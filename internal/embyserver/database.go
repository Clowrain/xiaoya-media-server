package embyserver

import (
	"database/sql"
	_ "github.com/lib/pq"
	"strconv"
	"strings"
	"time"
)

// All application SQL uses bound parameters. Literal question marks are preserved.
func bind(q string) string {
	var b strings.Builder
	quoted := false
	n := 0
	for i := 0; i < len(q); i++ {
		c := q[i]
		if c == '\'' {
			if quoted && i+1 < len(q) && q[i+1] == '\'' {
				b.WriteString("''")
				i++
				continue
			}
			quoted = !quoted
		}
		if c == '?' && !quoted {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

type Database struct{ *sql.DB }
type Transaction struct{ *sql.Tx }

func openDatabase(dsn string) (*Database, error) {
	d, e := sql.Open("postgres", dsn)
	if e != nil {
		return nil, e
	}
	d.SetMaxOpenConns(6)
	d.SetMaxIdleConns(2)
	d.SetConnMaxIdleTime(time.Minute)
	d.SetConnMaxLifetime(30 * time.Minute)
	if e = d.Ping(); e != nil {
		d.Close()
		return nil, e
	}
	return &Database{d}, nil
}
func (d *Database) Exec(q string, a ...any) (sql.Result, error) {
	return d.DB.Exec(bind(q), pgArgs(a)...)
}
func (d *Database) Query(q string, a ...any) (*sql.Rows, error) {
	return d.DB.Query(bind(q), pgArgs(a)...)
}
func (d *Database) QueryRow(q string, a ...any) *sql.Row { return d.DB.QueryRow(bind(q), pgArgs(a)...) }
func (d *Database) Begin() (*Transaction, error) {
	t, e := d.DB.Begin()
	if e != nil {
		return nil, e
	}
	return &Transaction{t}, nil
}
func (t *Transaction) Exec(q string, a ...any) (sql.Result, error) {
	return t.Tx.Exec(bind(q), pgArgs(a)...)
}
func (t *Transaction) Query(q string, a ...any) (*sql.Rows, error) {
	return t.Tx.Query(bind(q), pgArgs(a)...)
}
func (t *Transaction) QueryRow(q string, a ...any) *sql.Row {
	return t.Tx.QueryRow(bind(q), pgArgs(a)...)
}
func (t *Transaction) Prepare(q string) (*sql.Stmt, error) { return t.Tx.Prepare(bind(q)) }

func pgArgs(a []any) []any {
	for i, v := range a {
		if b, ok := v.(bool); ok {
			if b {
				a[i] = int64(1)
			} else {
				a[i] = int64(0)
			}
		}
	}
	return a
}
