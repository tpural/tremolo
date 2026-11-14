package runbooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrDecided is a proposal that was already approved or rejected.
var ErrDecided = errors.New("that proposal was already decided")

// Proposal is an agent's runbook edit, waiting for a person to approve it.
type Proposal struct {
	ID int64 `json:"id"`
	// Runbook is what the runbook would be.
	Runbook Runbook `json:"runbook"`
	// Base is the version it edits, or 0 for a new runbook.
	Base      int       `json:"base"`
	By        string    `json:"by"`
	Created   time.Time `json:"created"`
	Status    string    `json:"status"`
	Decided   time.Time `json:"decided,omitzero"`
	DecidedBy string    `json:"decidedBy,omitempty"`
}

// Propose keeps rb as a proposed edit, or a proposed new runbook when create
// is set, without changing any runbook.
func (s *Store) Propose(ctx context.Context, rb Runbook, by string, create bool) (Proposal, error) {
	if create && rb.ID == "" {
		rb.ID = Slug(rb.Title)
	}
	if err := clean(&rb); err != nil {
		return Proposal{}, err
	}
	p := Proposal{By: by, Created: time.Now(), Status: "pending"}
	current, err := s.Get(ctx, rb.ID)
	switch {
	case create && err == nil:
		return p, ErrExists
	case create && errors.Is(err, ErrNotFound):
	case err != nil:
		return p, err
	case rb.Version != 0 && rb.Version != current.Version:
		return p, ErrChanged
	default:
		p.Base = current.Version
	}
	rb.Version, rb.Updated, rb.UpdatedBy = p.Base, p.Created, by
	p.Runbook = rb
	rules, _ := json.Marshal(rb.Rules)
	// The same edit again is the one already waiting: agents retry.
	same, err := scanProposal(s.db.QueryRowContext(ctx, proposalColumns+` WHERE status = 'pending' AND runbook = ? AND base = ?
		AND title = ? AND body = ? AND rules = ? AND by = ?`, rb.ID, p.Base, rb.Title, rb.Body, string(rules), by))
	if err == nil {
		return same, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO runbook_proposals (runbook, base, title, body, rules, created, by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rb.ID, p.Base, rb.Title, rb.Body, string(rules), p.Created.UnixMilli(), by)
	if err != nil {
		return p, err
	}
	p.ID, err = res.LastInsertId()
	return p, err
}

// Proposals returns proposals with status, or all of them, newest first.
func (s *Store) Proposals(ctx context.Context, status string) ([]Proposal, error) {
	rows, err := s.db.QueryContext(ctx, proposalColumns+` WHERE ? = '' OR status = ? ORDER BY created DESC, id DESC LIMIT 200`, status, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Proposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Approve applies a pending proposal, as an edit by the agent that proposed
// it. It fails with ErrChanged if the runbook changed since.
func (s *Store) Approve(ctx context.Context, id int64, by string) (Runbook, error) {
	p, err := s.pending(ctx, id)
	if err != nil {
		return Runbook{}, err
	}
	var rb Runbook
	if p.Base == 0 {
		rb, err = s.Create(ctx, p.Runbook, p.By)
	} else {
		p.Runbook.Version = p.Base
		rb, err = s.Update(ctx, p.Runbook, p.By)
	}
	if err != nil {
		return rb, err
	}
	return rb, s.decide(ctx, id, "approved", by)
}

// Reject turns a pending proposal down.
func (s *Store) Reject(ctx context.Context, id int64, by string) error {
	if _, err := s.pending(ctx, id); err != nil {
		return err
	}
	return s.decide(ctx, id, "rejected", by)
}

func (s *Store) pending(ctx context.Context, id int64) (Proposal, error) {
	p, err := scanProposal(s.db.QueryRowContext(ctx, proposalColumns+` WHERE id = ?`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return p, ErrNotFound
	case err != nil:
		return p, err
	case p.Status != "pending":
		return p, ErrDecided
	}
	return p, nil
}

func (s *Store) decide(ctx context.Context, id int64, status, by string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE runbook_proposals SET status = ?, decided = ?, decided_by = ? WHERE id = ? AND status = 'pending'`,
		status, time.Now().UnixMilli(), by, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrDecided
	}
	return nil
}

const proposalColumns = `SELECT id, runbook, base, title, body, rules, created, by, status, decided, decided_by FROM runbook_proposals`

func scanProposal(row scanner) (Proposal, error) {
	var p Proposal
	var rules string
	var created, decided int64
	if err := row.Scan(&p.ID, &p.Runbook.ID, &p.Base, &p.Runbook.Title, &p.Runbook.Body, &rules, &created, &p.By, &p.Status, &decided, &p.DecidedBy); err != nil {
		return p, err
	}
	_ = json.Unmarshal([]byte(rules), &p.Runbook.Rules)
	p.Created = time.UnixMilli(created)
	p.Runbook.Version, p.Runbook.Updated, p.Runbook.UpdatedBy = p.Base, p.Created, p.By
	if decided > 0 {
		p.Decided = time.UnixMilli(decided)
	}
	return p, nil
}
