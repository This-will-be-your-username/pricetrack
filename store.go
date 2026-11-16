package main

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
create table if not exists items(
  id            integer primary key autoincrement,
  name          text not null,
  image         text not null default '',
  currency      text not null default 'USD',
  target        real,
  last_searched integer not null default 0,
  last_error    text not null default '',
  created_at    integer not null
);
create table if not exists listings(
  id           integer primary key autoincrement,
  item_id      integer not null references items(id) on delete cascade,
  url          text not null,
  retailer     text not null default '',
  title        text not null default '',
  price        real,
  currency     text not null default '',
  last_checked integer not null default 0,
  last_error   text not null default '',
  caveat       text not null default '',
  std_price    real,
  manual       integer not null default 0,
  unique(item_id, url)
);
create table if not exists history(
  id      integer primary key autoincrement,
  item_id integer not null references items(id) on delete cascade,
  price   real not null,
  ts      integer not null
);
create index if not exists history_item on history(item_id, id);
create table if not exists settings(key text primary key, value text not null);
`

// Item is a product being tracked across retailers.
type Item struct {
	ID           int64
	Name         string
	Image        string
	Currency     string
	Target       float64
	HasTarget    bool
	LastSearched time.Time
	LastError    string
	CreatedAt    time.Time
}

// Listing is one retailer's offer for an item.
type Listing struct {
	ID          int64
	ItemID      int64
	URL         string
	Retailer    string
	Title       string
	Price       float64
	HasPrice    bool
	Currency    string
	LastChecked time.Time
	LastError   string
	Caveat      string  // condition on Price, e.g. "Member price"
	StdPrice    float64 // cheapest unconditional price when Caveat is set
	HasStd      bool
	Manual      bool // price was typed in by the user, not read from the page
}

// Fresh reports whether the price was read or entered within the last hour.
func (l *Listing) Fresh() bool {
	return l.HasPrice && l.LastError == "" && !l.LastChecked.IsZero() && time.Since(l.LastChecked) < time.Hour
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
	// Upgrade databases created before conditional pricing existed.
	for _, q := range []string{`alter table listings add column caveat text not null default ''`, `alter table listings add column std_price real`, `alter table listings add column manual integer not null default 0`} {
		if _, err := db.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, err
		}
	}
	s := &Store{db: db}
	if err := s.migrateV1(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrateV1 converts the original single-URL products table into items with one listing each.
func (s *Store) migrateV1() error {
	var n int
	if err := s.db.QueryRow(`select count(*) from sqlite_master where type='table' and name='products'`).Scan(&n); err != nil || n == 0 {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type old struct {
		id, created, checked            int64
		url, name, merchant, image, cur string
		price, target                   sql.NullFloat64
	}
	rows, err := tx.Query(`select id, url, name, merchant, image, currency, price, target, last_checked, created_at from products`)
	if err != nil {
		return err
	}
	var olds []old
	for rows.Next() {
		var o old
		if err := rows.Scan(&o.id, &o.url, &o.name, &o.merchant, &o.image, &o.cur, &o.price, &o.target, &o.checked, &o.created); err != nil {
			rows.Close()
			return err
		}
		olds = append(olds, o)
	}
	rows.Close()
	for _, o := range olds {
		name := o.name
		if name == "" || name == o.url {
			name = o.merchant + " product"
		}
		res, err := tx.Exec(`insert into items(name, image, currency, target, created_at) values(?,?,?,?,?)`,
			name, o.image, o.cur, o.target, o.created)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if _, err := tx.Exec(`insert into listings(item_id, url, retailer, title, price, currency, last_checked) values(?,?,?,?,?,?,?)`,
			id, o.url, o.merchant, name, o.price, o.cur, o.checked); err != nil {
			return err
		}
		if _, err := tx.Exec(`insert into history(item_id, price, ts) select ?, price, ts from prices where product_id = ? order by id`, id, o.id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`drop table if exists prices; drop table products;`); err != nil {
		return err
	}
	return tx.Commit()
}

type scanner interface{ Scan(...any) error }

const itemCols = `id, name, image, currency, target, last_searched, last_error, created_at`

func scanItem(r scanner) (*Item, error) {
	var it Item
	var target sql.NullFloat64
	var searched, created int64
	if err := r.Scan(&it.ID, &it.Name, &it.Image, &it.Currency, &target, &searched, &it.LastError, &created); err != nil {
		return nil, err
	}
	it.Target, it.HasTarget = target.Float64, target.Valid
	if searched > 0 {
		it.LastSearched = time.Unix(searched, 0)
	}
	it.CreatedAt = time.Unix(created, 0)
	return &it, nil
}

const listingCols = `id, item_id, url, retailer, title, price, currency, last_checked, last_error, caveat, std_price, manual`

func scanListing(r scanner) (*Listing, error) {
	var l Listing
	var price, std sql.NullFloat64
	var checked, manual int64
	if err := r.Scan(&l.ID, &l.ItemID, &l.URL, &l.Retailer, &l.Title, &price, &l.Currency, &checked, &l.LastError, &l.Caveat, &std, &manual); err != nil {
		return nil, err
	}
	l.Price, l.HasPrice = price.Float64, price.Valid
	l.StdPrice, l.HasStd = std.Float64, std.Valid
	l.Manual = manual != 0
	if checked > 0 {
		l.LastChecked = time.Unix(checked, 0)
	}
	return &l, nil
}

func (s *Store) AddItem(name, currency string, target *float64) (int64, error) {
	res, err := s.db.Exec(`insert into items(name, currency, target, created_at) values(?,?,?,?)`,
		name, currency, target, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetItem(id int64) (*Item, error) {
	return scanItem(s.db.QueryRow(`select `+itemCols+` from items where id = ?`, id))
}

func (s *Store) ListItems() ([]*Item, error) {
	rows, err := s.db.Query(`select ` + itemCols + ` from items order by created_at desc, id desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) DeleteItem(id int64) error {
	_, err := s.db.Exec(`delete from items where id = ?`, id)
	return err
}

func (s *Store) SetTarget(id int64, target *float64) error {
	_, err := s.db.Exec(`update items set target = ? where id = ?`, target, id)
	return err
}

func (s *Store) SetItemImage(id int64, img string) {
	s.db.Exec(`update items set image = ? where id = ? and image = ''`, img, id)
}

func (s *Store) MarkSearched(id int64, serr error) error {
	msg := ""
	if serr != nil {
		msg = clip(serr.Error())
	}
	_, err := s.db.Exec(`update items set last_searched = ?, last_error = ? where id = ?`, time.Now().Unix(), msg, id)
	return err
}

func (s *Store) ListListings(itemID int64) ([]*Listing, error) {
	rows, err := s.db.Query(`select `+listingCols+` from listings where item_id = ? order by price is null, price, id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Listing
	for rows.Next() {
		l, err := scanListing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) GetListing(id int64) (*Listing, error) {
	return scanListing(s.db.QueryRow(`select `+listingCols+` from listings where id = ?`, id))
}

func (s *Store) DeleteListing(id int64) error {
	_, err := s.db.Exec(`delete from listings where id = ?`, id)
	return err
}

// UpsertListing inserts or refreshes a listing. A non-positive price stores NULL.
func (s *Store) UpsertListing(itemID int64, url, retailer, title string, price float64, cur, caveat string, stdPrice float64) error {
	var p, sp any
	if price > 0 {
		p = price
	}
	if stdPrice > 0 && caveat != "" {
		sp = stdPrice
	}
	_, err := s.db.Exec(`insert into listings(item_id, url, retailer, title, price, currency, last_checked, caveat, std_price)
		values(?,?,?,?,?,?,?,?,?)
		on conflict(item_id, url) do update set retailer = excluded.retailer,
		  title = case when excluded.title != '' then excluded.title else title end,
		  price = coalesce(excluded.price, price), currency = excluded.currency,
		  manual = case when excluded.price is null then manual else 0 end,
		  caveat = case when excluded.price is null then caveat else excluded.caveat end,
		  std_price = case when excluded.price is null then std_price else excluded.std_price end,
		  last_checked = excluded.last_checked, last_error = ''`,
		itemID, url, retailer, title, p, cur, time.Now().Unix(), caveat, sp)
	return err
}

// MarkListingError records why a listing's price couldn't be read.
func (s *Store) MarkListingError(itemID int64, url, msg string) {
	s.db.Exec(`update listings set last_error = ? where item_id = ? and url = ? and price is null`, clip(msg), itemID, url)
}

// SetManualPrice stores a price the user checked by hand. A non-positive price clears it.
func (s *Store) SetManualPrice(id int64, price float64, cur string) error {
	if price <= 0 {
		_, err := s.db.Exec(`update listings set price = null, manual = 0, caveat = '', std_price = null where id = ? and manual = 1`, id)
		return err
	}
	_, err := s.db.Exec(`update listings set price = ?, currency = ?, manual = 1, caveat = '', std_price = null,
		last_checked = ?, last_error = '' where id = ?`, price, cur, time.Now().Unix(), id)
	return err
}

// UpdateListing stores the outcome of re-checking a listing.
func (s *Store) UpdateListing(id int64, r *Result, ferr error) error {
	now := time.Now().Unix()
	if r != nil && r.Image != "" {
		s.db.Exec(`update items set image = ? where image = '' and id = (select item_id from listings where id = ?)`, r.Image, id)
	}
	if ferr != nil {
		_, err := s.db.Exec(`update listings set last_checked = ?, last_error = ? where id = ?`, now, clip(ferr.Error()), id)
		return err
	}
	var sp any
	if r.Caveat != "" && r.StdPrice > 0 {
		sp = r.StdPrice
	}
	_, err := s.db.Exec(`update listings set price = ?, currency = ?, title = case when ? != '' then ? else title end,
		caveat = ?, std_price = ?, manual = 0, last_checked = ?, last_error = '' where id = ?`, r.Price, r.Currency, r.Name, r.Name, r.Caveat, sp, now, id)
	return err
}

// BestListing returns the cheapest priced listing in the item's currency.
func bestOf(cur string, ls []*Listing) *Listing {
	var best *Listing
	for _, l := range ls {
		if l.HasPrice && l.Currency == cur && (best == nil || l.Price < best.Price) {
			best = l
		}
	}
	return best
}

// RecordBest appends the current cheapest price to the item's history when it changed.
func (s *Store) RecordBest(itemID int64) error {
	it, err := s.GetItem(itemID)
	if err != nil {
		return err
	}
	ls, err := s.ListListings(itemID)
	if err != nil {
		return err
	}
	best := bestOf(it.Currency, ls)
	if best == nil { // adopt the currency of the first priced listing
		for _, l := range ls {
			if l.HasPrice && l.Currency != "" {
				if _, err := s.db.Exec(`update items set currency = ? where id = ?`, l.Currency, itemID); err != nil {
					return err
				}
				best = bestOf(l.Currency, ls)
				break
			}
		}
	}
	if best == nil {
		return nil
	}
	var last float64
	err = s.db.QueryRow(`select price from history where item_id = ? order by id desc limit 1`, itemID).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && last != best.Price) {
		_, err = s.db.Exec(`insert into history(item_id, price, ts) values(?,?,?)`, itemID, best.Price, time.Now().Unix())
	}
	return err
}

// History returns best-price points, oldest first.
func (s *Store) History(id int64) ([]float64, error) {
	rows, err := s.db.Query(`select price from history where item_id = ? order by id`, id)
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

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
