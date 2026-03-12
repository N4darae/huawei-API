package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type nodeRepo struct{ base }

const nodeCols = `id, name, kind, public_host, created_at, updated_at`

func (r *nodeRepo) Get(ctx context.Context, id string) (domain.Node, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id = ?`, id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Node{}, notFound("node", id)
	}
	if err != nil {
		return domain.Node{}, mapErr(err, "get node")
	}
	return n, nil
}

func (r *nodeRepo) List(ctx context.Context) ([]domain.Node, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT `+nodeCols+` FROM nodes ORDER BY name`)
	if err != nil {
		return nil, mapErr(err, "list nodes")
	}
	defer rows.Close()

	out := []domain.Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, mapErr(err, "scan node")
		}
		out = append(out, n)
	}
	return out, mapErr(rows.Err(), "list nodes")
}

func (r *nodeRepo) Upsert(ctx context.Context, n domain.Node) error {
	if n.ID == "" {
		return errInvalid("node id is required")
	}
	if n.Kind == "" {
		n.Kind = domain.NodeKindLocal
	}
	created, updated := r.stamps(n.CreatedAt)
	return r.exec(ctx, "upsert node",
		`INSERT INTO nodes(`+nodeCols+`) VALUES(?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name,
		   kind = excluded.kind,
		   public_host = excluded.public_host,
		   updated_at = excluded.updated_at`,
		n.ID, n.Name, n.Kind, addrText(n.PublicHost), created, updated)
}

type scanner interface{ Scan(dest ...any) error }

func scanNode(s scanner) (domain.Node, error) {
	var (
		n    domain.Node
		host string
	)
	if err := s.Scan(&n.ID, &n.Name, &n.Kind, &host, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return domain.Node{}, err
	}
	a, err := parseAddr("node public_host", host)
	if err != nil {
		return domain.Node{}, err
	}
	n.PublicHost = a
	return n, nil
}
