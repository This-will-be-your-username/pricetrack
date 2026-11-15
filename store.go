package main

import (
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
create table if not exists products(
  id           integer primary key autoincrement,
  url          text not null unique,
  name         text not null default '',
  merchant     text not null default '',
  image        text not null default '',
  currency     text not null default 'USD',
  price        real,
  target       real,
  last_checked integer not null default 0,
  last_error   text not null default '',
  created_at   integer not null
);
create table if not exists prices(
  id         integer primary key autoincrement,
  product_id integer not null references products(id) on delete cascade,
  price      real not null,
  ts         integer not null
);
create table if not exists settings(key text primary key, value text not null);
create index if not exists prices_pid on prices(product_id, id);
`

// Product is a tracked item.
type Product struct {
	ID          int64
	URL         string
	Name        string
	Merchant    string
	Image       string
	Currency    string
	Price       float64
	HasPrice    bool
	Target      float64
	HasTarget   bool
	LastChecked time.Time
	LastError   string
	CreatedAt   time.Time
}

// Store wraps the SQLite database.
type Store struct{ db *sql.DB }

func OpenStore(path string) (*Store, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

const cols = `id, url, name, merchant, image, currency, price, target, last_checked, last_error, created_at`

type scanner interface{ Scan(...any) error }

func scanProduct(r scanner) (*Product, error) {
	var p Product
	var price, target sql.NullFloat64
	var checked, created int64
	if err := r.Scan(&p.ID, &p.URL, &p.Name, &p.Merchant, &p.Image, &p.Currency,
		&price, &target, &checked, &p.LastError, &created); err != nil {
		return nil, err
	}
	p.Price, p.HasPrice = price.Float64, price.Valid
	p.Target, p.HasTarget = target.Float64, target.Valid
	if checked > 0 {
		p.LastChecked = time.Unix(checked, 0)
	}
	p.CreatedAt = time.Unix(created, 0)
	return &p, nil
}

var ErrExists = errors.New("already tracking this product")

func (s *Store) AddProduct(url, merchant, name string, target *float64) (int64, error) {
	var exists int
	if err := s.db.QueryRow(`select count(*) from products where url = ?`, url).Scan(&exists); err != nil {
		return 0, err
	}
	if exists > 0 {
		return 0, ErrExists
	}
	res, err := s.db.Exec(`insert into products(url, merchant, name, target, created_at) values(?,?,?,?,?)`,
		url, merchant, name, target, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetProduct(id int64) (*Product, error) {
	return scanProduct(s.db.QueryRow(`select `+cols+` from products where id = ?`, id))
}

func (s *Store) ListProducts() ([]*Product, error) {
	rows, err := s.db.Query(`select ` + cols + ` from products order by created_at desc, id desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetSetting(key string) string {
	var v string
	s.db.QueryRow(`select value from settings where key = ?`, key).Scan(&v)
	return v
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`insert into settings(key, value) values(?,?)
		on conflict(key) do update set value = excluded.value`, key, value)
	return err
}

func (s *Store) DeleteProduct(id int64) error {
	_, err := s.db.Exec(`delete from products where id = ?`, id)
	return err
}

func (s *Store) SetTarget(id int64, target *float64) error {
	_, err := s.db.Exec(`update products set target = ? where id = ?`, target, id)
	return err
}

// History returns recorded prices, oldest first.
func (s *Store) History(id int64) ([]float64, error) {
	rows, err := s.db.Query(`select price from prices where product_id = ? order by id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []float64
	for rows.Next() {
		var p float64
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecordCheck stores the outcome of a fetch. r may be partial when ferr != nil.
func (s *Store) RecordCheck(id int64, r *Result, ferr error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()

	if r != nil {
		if _, err := tx.Exec(`update products set
			name = case when ? != '' then ? else name end,
			image = case when ? != '' then ? else image end
			where id = ?`,
			r.Name, r.Name, r.Image, r.Image, id); err != nil {
			return err
		}
	}
	if ferr != nil {
		msg := ferr.Error()
		if len(msg) > 300 {
			msg = msg[:300]
		}
		if _, err := tx.Exec(`update products set last_checked = ?, last_error = ? where id = ?`, now, msg, id); err != nil {
			return err
		}
		return tx.Commit()
	}

	if _, err := tx.Exec(`update products set price = ?, currency = ?, last_checked = ?, last_error = '' where id = ?`,
		r.Price, r.Currency, now, id); err != nil {
		return err
	}
	var last float64
	err = tx.QueryRow(`select price from prices where product_id = ? order by id desc limit 1`, id).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && last != r.Price) {
		if _, err := tx.Exec(`insert into prices(product_id, price, ts) values(?,?,?)`, id, r.Price, now); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return tx.Commit()
}
