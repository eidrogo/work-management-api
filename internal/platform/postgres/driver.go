package postgres

/*
#cgo pkg-config: libpq
#include <libpq-fe.h>
#include <stdlib.h>
#include <poll.h>

static int wait_readable(int fd, int timeout_ms) {
    struct pollfd pfd;
    pfd.fd = fd;
    pfd.events = POLLIN;
    pfd.revents = 0;
    return poll(&pfd, 1, timeout_ms);
}
*/
import "C"

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const DriverName = "work_management_libpq"

func init() { sql.Register(DriverName, &pqDriver{}) }

type Error struct {
	Severity   string
	Code       string
	Message    string
	Detail     string
	Constraint string
}

func (e *Error) Error() string {
	if e == nil || e.Message == "" {
		return "PostgreSQL error"
	}
	return e.Message
}

type pqDriver struct{}

func (*pqDriver) Open(name string) (driver.Conn, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	rawConn := C.PQconnectdb(cName)
	if rawConn == nil {
		return nil, errors.New("PostgreSQL connection allocation failed")
	}
	if C.PQstatus(rawConn) != C.CONNECTION_OK {
		err := errorFromConn(rawConn)
		C.PQfinish(rawConn)
		return nil, err
	}
	return &conn{raw: rawConn}, nil
}

type conn struct {
	mu     sync.Mutex
	raw    *C.PGconn
	closed bool
}

var (
	_ driver.Conn              = (*conn)(nil)
	_ driver.ExecerContext     = (*conn)(nil)
	_ driver.QueryerContext    = (*conn)(nil)
	_ driver.ConnBeginTx       = (*conn)(nil)
	_ driver.Pinger            = (*conn)(nil)
	_ driver.NamedValueChecker = (*conn)(nil)
	_ driver.Validator         = (*conn)(nil)
	_ driver.SessionResetter   = (*conn)(nil)
)

func (c *conn) Prepare(query string) (driver.Stmt, error) { return &stmt{conn: c, query: query}, nil }

func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.raw != nil {
		C.PQfinish(c.raw)
		c.raw = nil
	}
	return nil
}

func (c *conn) Begin() (driver.Tx, error) { return c.BeginTx(context.Background(), driver.TxOptions{}) }

func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	isolation := ""
	switch opts.Isolation {
	case driver.IsolationLevel(sql.LevelDefault):
	case driver.IsolationLevel(sql.LevelReadCommitted):
		isolation = " ISOLATION LEVEL READ COMMITTED"
	case driver.IsolationLevel(sql.LevelRepeatableRead):
		isolation = " ISOLATION LEVEL REPEATABLE READ"
	case driver.IsolationLevel(sql.LevelSerializable):
		isolation = " ISOLATION LEVEL SERIALIZABLE"
	default:
		return nil, fmt.Errorf("unsupported transaction isolation level %d", opts.Isolation)
	}
	readMode := ""
	if opts.ReadOnly {
		readMode = " READ ONLY"
	}
	results, err := c.run(ctx, "BEGIN"+isolation+readMode, nil)
	if err != nil {
		return nil, err
	}
	clearResults(results)
	return &tx{conn: c}, nil
}

func (c *conn) Ping(ctx context.Context) error {
	results, err := c.run(ctx, "SELECT 1", nil)
	if err != nil {
		return err
	}
	clearResults(results)
	return nil
}

func (c *conn) ResetSession(context.Context) error {
	if !c.IsValid() {
		return driver.ErrBadConn
	}
	return nil
}

func (c *conn) IsValid() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.raw != nil && C.PQstatus(c.raw) == C.CONNECTION_OK
}

func (c *conn) CheckNamedValue(value *driver.NamedValue) error {
	if value.Name != "" {
		return errors.New("named PostgreSQL parameters are not supported")
	}
	switch value.Value.(type) {
	case nil, int64, float64, bool, []byte, string, time.Time, int, int32, uint, uint32, uint64:
		return nil
	default:
		return driver.ErrSkip
	}
}

func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	results, err := c.run(ctx, query, args)
	if err != nil {
		return nil, err
	}
	defer clearResults(results)
	var affected int64
	for _, result := range results {
		status := C.PQresultStatus(result)
		if status != C.PGRES_COMMAND_OK && status != C.PGRES_TUPLES_OK {
			return nil, errorFromResult(result)
		}
		if tuples := C.GoString(C.PQcmdTuples(result)); tuples != "" {
			count, parseErr := strconv.ParseInt(tuples, 10, 64)
			if parseErr != nil {
				return nil, fmt.Errorf("parse PostgreSQL affected rows: %w", parseErr)
			}
			affected += count
		}
	}
	return result{rowsAffected: affected}, nil
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	results, err := c.run(ctx, query, args)
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		clearResults(results)
		return nil, fmt.Errorf("query returned %d result sets; expected one", len(results))
	}
	if status := C.PQresultStatus(results[0]); status != C.PGRES_TUPLES_OK {
		err := errorFromResult(results[0])
		clearResults(results)
		return nil, err
	}
	return newRows(results[0]), nil
}

func (c *conn) run(ctx context.Context, query string, args []driver.NamedValue) ([]*C.PGresult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.raw == nil {
		return nil, driver.ErrBadConn
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cQuery := C.CString(query)
	defer C.free(unsafe.Pointer(cQuery))
	var sent C.int
	var cValues []*C.char
	if len(args) == 0 {
		sent = C.PQsendQuery(c.raw, cQuery)
	} else {
		cValues = make([]*C.char, len(args))
		for index, argument := range args {
			encoded, isNull, err := encodeParameter(argument.Value)
			if err != nil {
				freeValues(cValues)
				return nil, err
			}
			if !isNull {
				cValues[index] = C.CString(encoded)
			}
		}
		defer freeValues(cValues)
		sent = C.PQsendQueryParams(c.raw, cQuery, C.int(len(cValues)), nil,
			(**C.char)(unsafe.Pointer(&cValues[0])), nil, nil, 0)
	}
	if sent == 0 {
		return nil, errorFromConn(c.raw)
	}

	for {
		if err := ctx.Err(); err != nil {
			c.cancelAndInvalidate()
			return nil, err
		}
		if C.PQconsumeInput(c.raw) == 0 {
			err := errorFromConn(c.raw)
			c.invalidate()
			return nil, err
		}
		if C.PQisBusy(c.raw) == 0 {
			break
		}
		fd := int(C.PQsocket(c.raw))
		if fd < 0 {
			c.invalidate()
			return nil, errors.New("PostgreSQL connection has no socket")
		}
		waitResult := int(C.wait_readable(C.int(fd), 100))
		if waitResult < 0 {
			c.invalidate()
			return nil, errors.New("wait for PostgreSQL response failed")
		}
	}

	var results []*C.PGresult
	for {
		result := C.PQgetResult(c.raw)
		if result == nil {
			break
		}
		status := C.PQresultStatus(result)
		if status == C.PGRES_FATAL_ERROR || status == C.PGRES_BAD_RESPONSE || status == C.PGRES_NONFATAL_ERROR {
			err := errorFromResult(result)
			C.PQclear(result)
			clearResults(results)
			return nil, err
		}
		results = append(results, result)
	}
	if len(results) == 0 {
		return nil, errors.New("PostgreSQL returned no result")
	}
	return results, nil
}

func (c *conn) cancelAndInvalidate() {
	if c.raw == nil {
		return
	}
	cancel := C.PQgetCancel(c.raw)
	if cancel != nil {
		buffer := make([]byte, 256)
		C.PQcancel(cancel, (*C.char)(unsafe.Pointer(&buffer[0])), C.int(len(buffer)))
		C.PQfreeCancel(cancel)
	}
	c.invalidate()
}

func (c *conn) invalidate() {
	if c.raw != nil {
		C.PQfinish(c.raw)
		c.raw = nil
	}
	c.closed = true
}

type stmt struct {
	conn  *conn
	query string
}

func (s *stmt) Close() error  { return nil }
func (s *stmt) NumInput() int { return -1 }
func (s *stmt) Exec(values []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), valuesToNamed(values))
}
func (s *stmt) Query(values []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), valuesToNamed(values))
}
func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.conn.ExecContext(ctx, s.query, args)
}
func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.conn.QueryContext(ctx, s.query, args)
}

type tx struct{ conn *conn }

func (t *tx) Commit() error {
	results, err := t.conn.run(context.Background(), "COMMIT", nil)
	if err != nil {
		return err
	}
	clearResults(results)
	return nil
}
func (t *tx) Rollback() error {
	results, err := t.conn.run(context.Background(), "ROLLBACK", nil)
	if err != nil {
		return err
	}
	clearResults(results)
	return nil
}

type result struct{ rowsAffected int64 }

func (result) LastInsertId() (int64, error)   { return 0, errors.New("use RETURNING with PostgreSQL") }
func (r result) RowsAffected() (int64, error) { return r.rowsAffected, nil }

type rows struct {
	raw     *C.PGresult
	columns []string
	index   int
	count   int
}

func newRows(raw *C.PGresult) *rows {
	columnCount := int(C.PQnfields(raw))
	columns := make([]string, columnCount)
	for index := 0; index < columnCount; index++ {
		columns[index] = C.GoString(C.PQfname(raw, C.int(index)))
	}
	return &rows{raw: raw, columns: columns, count: int(C.PQntuples(raw))}
}

func (r *rows) Columns() []string { return append([]string(nil), r.columns...) }
func (r *rows) Close() error {
	if r.raw != nil {
		C.PQclear(r.raw)
		r.raw = nil
	}
	return nil
}
func (r *rows) Next(destination []driver.Value) error {
	if r.raw == nil || r.index >= r.count {
		return io.EOF
	}
	for column := range destination {
		if C.PQgetisnull(r.raw, C.int(r.index), C.int(column)) != 0 {
			destination[column] = nil
			continue
		}
		length := int(C.PQgetlength(r.raw, C.int(r.index), C.int(column)))
		raw := C.GoBytes(unsafe.Pointer(C.PQgetvalue(r.raw, C.int(r.index), C.int(column))), C.int(length))
		value, err := decodeValue(uint32(C.PQftype(r.raw, C.int(column))), string(raw))
		if err != nil {
			return err
		}
		destination[column] = value
	}
	r.index++
	return nil
}

func encodeParameter(value any) (string, bool, error) {
	switch typed := value.(type) {
	case nil:
		return "", true, nil
	case string:
		return typed, false, nil
	case []byte:
		return `\x` + hex.EncodeToString(typed), false, nil
	case bool:
		return strconv.FormatBool(typed), false, nil
	case int:
		return strconv.Itoa(typed), false, nil
	case int32:
		return strconv.FormatInt(int64(typed), 10), false, nil
	case int64:
		return strconv.FormatInt(typed, 10), false, nil
	case uint:
		return strconv.FormatUint(uint64(typed), 10), false, nil
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), false, nil
	case uint64:
		return strconv.FormatUint(typed, 10), false, nil
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64), false, nil
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano), false, nil
	default:
		return "", false, fmt.Errorf("unsupported PostgreSQL parameter type %T", value)
	}
}

func decodeValue(oid uint32, value string) (driver.Value, error) {
	switch oid {
	case 16:
		return value == "t" || strings.EqualFold(value, "true"), nil
	case 20, 21, 23, 26:
		return strconv.ParseInt(value, 10, 64)
	case 700, 701:
		return strconv.ParseFloat(value, 64)
	case 17:
		if strings.HasPrefix(value, `\x`) {
			return hex.DecodeString(value[2:])
		}
		return []byte(value), nil
	case 1082:
		return time.Parse("2006-01-02", value)
	case 1114:
		return parseTimestamp(value, false)
	case 1184:
		return parseTimestamp(value, true)
	case 114, 3802:
		return []byte(value), nil
	default:
		return value, nil
	}
}

func parseTimestamp(value string, withZone bool) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999Z07",
		"2006-01-02 15:04:05Z07",
		time.RFC3339Nano,
	}
	if !withZone {
		layouts = append(layouts, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05")
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse PostgreSQL timestamp %q", value)
}

func valuesToNamed(values []driver.Value) []driver.NamedValue {
	result := make([]driver.NamedValue, len(values))
	for index, value := range values {
		result[index] = driver.NamedValue{Ordinal: index + 1, Value: value}
	}
	return result
}

func freeValues(values []*C.char) {
	for _, value := range values {
		if value != nil {
			C.free(unsafe.Pointer(value))
		}
	}
}
func clearResults(results []*C.PGresult) {
	for _, result := range results {
		if result != nil {
			C.PQclear(result)
		}
	}
}
func errorFromConn(conn *C.PGconn) error {
	if conn == nil {
		return errors.New("PostgreSQL connection error")
	}
	message := strings.TrimSpace(C.GoString(C.PQerrorMessage(conn)))
	if message == "" {
		message = "PostgreSQL connection error"
	}
	return &Error{Message: message}
}
func errorFromResult(result *C.PGresult) error {
	field := func(code C.int) string {
		value := C.PQresultErrorField(result, code)
		if value == nil {
			return ""
		}
		return strings.TrimSpace(C.GoString(value))
	}
	message := field(C.PG_DIAG_MESSAGE_PRIMARY)
	if message == "" {
		message = strings.TrimSpace(C.GoString(C.PQresultErrorMessage(result)))
	}
	return &Error{
		Severity: field(C.PG_DIAG_SEVERITY), Code: field(C.PG_DIAG_SQLSTATE), Message: message,
		Detail: field(C.PG_DIAG_MESSAGE_DETAIL), Constraint: field(C.PG_DIAG_CONSTRAINT_NAME),
	}
}
