// Package database provides support for access the database.
package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq" // Calls init function.
	"go.opentelemetry.io/otel/trace"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound = errors.New("not found")
)

// Config is the required properties to use the database.
type Config struct {
	User       string
	Password   string
	Host       string
	Name       string
	DisableTLS bool
}

// Open knows how to open a database connection based on the configuration.
func Open(cfg Config) (*sqlx.DB, error) {
	sslMode := "require"
	if cfg.DisableTLS {
		sslMode = "disable"
	}

	q := make(url.Values)
	q.Set("sslmode", sslMode)
	q.Set("timezone", "utc")

	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     cfg.Host,
		Path:     cfg.Name,
		RawQuery: q.Encode(),
	}

	return sqlx.Open("postgres", u.String())
}

// StatusCheck returns nil if it can successfully talk to the database. It
// returns a non-nil error otherwise.
func StatusCheck(ctx context.Context, db *sqlx.DB) error {
	ctx, span := trace.SpanFromContext(ctx).Tracer().Start(ctx, "foundation.database.statuscheck")
	defer span.End()

	// First check we can ping the database.
	var pingError error
	maxAttempts := 20
	for attempts := 1; attempts <= maxAttempts; attempts++ {
		pingError = db.Ping()
		if pingError == nil {
			break
		}
		time.Sleep(time.Duration(attempts) * 100 * time.Millisecond)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	// Make sure we didn't timeout or be cancelled.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Run a simple query to determine connectivity. Running this query forces a
	// round trip through the database.
	const q = `SELECT true`
	var tmp bool
	return db.QueryRowContext(ctx, q).Scan(&tmp)
}

// NamedQuerySlice is a helper function for executing queries that return a
// collection of data to be unmarshaled into a slice.
func NamedQuerySlice(ctx context.Context, db *sqlx.DB, query string, data interface{}, dest interface{}) error {
	ctx, span := trace.SpanFromContext(ctx).Tracer().Start(ctx, "foundation.database.namedqueryslice")
	defer span.End()

	val := reflect.ValueOf(dest)
	if val.Kind() != reflect.Ptr || val.Elem().Kind() != reflect.Slice {
		return errors.New("must provide a pointer to a slice")
	}

	rows, err := db.NamedQueryContext(ctx, query, data)
	if err != nil {
		return err
	}

	slice := val.Elem()
	for rows.Next() {
		v := reflect.New(slice.Type().Elem())
		if err := rows.StructScan(v.Interface()); err != nil {
			return err
		}
		slice.Set(reflect.Append(slice, v.Elem()))
	}

	return nil
}

// NamedQueryStruct is a helper function for executing queries that return a
// single value to be unmarshalled into a struct type.
func NamedQueryStruct(ctx context.Context, db *sqlx.DB, query string, data interface{}, dest interface{}) error {
	ctx, span := trace.SpanFromContext(ctx).Tracer().Start(ctx, "foundation.database.namedquerystruct")
	defer span.End()

	rows, err := db.NamedQueryContext(ctx, query, data)
	if err != nil {
		return err
	}
	if !rows.Next() {
		return ErrNotFound
	}

	if err := rows.StructScan(dest); err != nil {
		return err
	}

	return nil
}

// Log provides a pretty print version of the query and parameters.
func Log(query string, args ...interface{}) string {
	query, params, err := sqlx.Named(query, args)
	if err != nil {
		return err.Error()
	}

	for _, param := range params {
		var value string
		switch v := param.(type) {
		case string:
			value = fmt.Sprintf("%q", v)
		case []byte:
			value = fmt.Sprintf("%q", string(v))
		default:
			value = fmt.Sprintf("%v", v)
		}
		query = strings.Replace(query, "?", value, 1)
	}

	query = strings.Replace(query, "\t", "", -1)
	query = strings.Replace(query, "\n", " ", -1)

	return fmt.Sprintf("[%s]\n", strings.Trim(query, " "))
}
