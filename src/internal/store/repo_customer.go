package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type customerRepo struct{ base }

const customerCols = `id, name, contact, note, created_at, updated_at`

func (r *customerRepo) Get(ctx context.Context, id string) (domain.Customer, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+customerCols+` FROM customers WHERE id = ?`, id)
	c, err := scanCustomer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Customer{}, notFound("customer", id)
	}
	if err != nil {
		return domain.Customer{}, mapErr(err, "get customer")
	}
	return c, nil
}

func (r *customerRepo) List(ctx context.Context) ([]domain.Customer, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT `+customerCols+` FROM customers ORDER BY name, id`)
	if err != nil {
		return nil, mapErr(err, "list customers")
	}
	defer rows.Close()

	out := []domain.Customer{}
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, mapErr(err, "scan customer")
		}
		out = append(out, c)
	}
	return out, mapErr(rows.Err(), "list customers")
}

func (r *customerRepo) Create(ctx context.Context, c domain.Customer) error {
	if c.ID == "" || c.Name == "" {
		return errInvalid("customer id and name are required")
	}
	created, updated := r.stamps(c.CreatedAt)
	return r.exec(ctx, "create customer",
		`INSERT INTO customers(`+customerCols+`) VALUES(?,?,?,?,?,?)`,
		c.ID, c.Name, c.Contact, c.Note, created, updated)
}

func (r *customerRepo) Update(ctx context.Context, c domain.Customer) error {
	if c.ID == "" {
		return errInvalid("customer id is required")
	}
	return r.execAffecting(ctx, "update customer", c.ID,
		`UPDATE customers SET name=?, contact=?, note=?, updated_at=? WHERE id=?`,
		c.Name, c.Contact, c.Note, r.now(), c.ID)
}

func (r *customerRepo) Delete(ctx context.Context, id string) error {
	return r.execAffecting(ctx, "delete customer", id, `DELETE FROM customers WHERE id = ?`, id)
}

func (r *customerRepo) CountProxies(ctx context.Context, id string) (int, error) {
	return r.count(ctx, "count customer proxies",
		`SELECT count(*) FROM proxies WHERE customer_id = ?`, id)
}

func scanCustomer(s scanner) (domain.Customer, error) {
	var c domain.Customer
	if err := s.Scan(&c.ID, &c.Name, &c.Contact, &c.Note, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return domain.Customer{}, err
	}
	return c, nil
}
