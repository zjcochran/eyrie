// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Page is a status page. Its sections list monitors: those with the
// section's tag, if it has one, and those it names.
type Page struct {
	ID             int64
	Slug           string // its address: /status/<slug>
	Title          string
	Description    string
	Footer         string
	Published      bool // false: only signed-in users see it
	Theme          string
	Accent         string // #rrggbb; "" for Eyrie's
	ShowTags       bool
	ShowCertExpiry bool
	Sections       []Section
	LogoUpdated    time.Time // zero: no logo
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Section is a part of a status page.
type Section struct {
	ID         int64
	Title      string
	TagID      int64 // 0: none
	MonitorIDs []int64
}

// Themes a status page can have.
var Themes = []string{"auto", "light", "dark"}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// cleanPage checks a page's settings. Errors are meant for people.
func cleanPage(p *Page) error {
	p.Slug, p.Title = strings.ToLower(strings.TrimSpace(p.Slug)), strings.TrimSpace(p.Title)
	p.Description, p.Footer = strings.TrimSpace(p.Description), strings.TrimSpace(p.Footer)
	p.Accent = strings.ToLower(strings.TrimSpace(p.Accent))
	switch {
	case len(p.Slug) == 0 || len(p.Slug) > 64 || !slugPattern.MatchString(p.Slug):
		return invalidf("the address must be 1 to 64 lower-case letters, digits and single dashes, such as shop-status")
	case p.Title == "" || len([]rune(p.Title)) > 100:
		return invalidf("the title must be 1 to 100 characters")
	case len(p.Description) > 2000 || len(p.Footer) > 2000:
		return invalidf("the description and the footer must be at most 2000 characters each")
	case !slices.Contains(Themes, p.Theme):
		return invalidf("the theme must be auto, light or dark")
	case p.Accent != "" && !hexColor.MatchString(p.Accent):
		return invalidf("the accent colour must be such as #1f5fbf")
	case len(p.Sections) > 50:
		return invalidf("a page has at most 50 sections")
	}
	for i := range p.Sections {
		p.Sections[i].Title = strings.TrimSpace(p.Sections[i].Title)
		if len([]rune(p.Sections[i].Title)) > 100 {
			return invalidf("a section's title must be at most 100 characters")
		}
	}
	return nil
}

// CreatePage saves a new status page with its sections.
func (s *Store) CreatePage(ctx context.Context, p Page, now time.Time) (Page, error) {
	if err := cleanPage(&p); err != nil {
		return Page{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	err = tx.QueryRowContext(ctx, s.q(`
		INSERT INTO status_pages (slug, title, description, footer, published, theme, accent, show_tags, show_cert_expiry,
			created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`),
		p.Slug, p.Title, p.Description, p.Footer, p.Published, p.Theme, p.Accent, p.ShowTags, p.ShowCertExpiry,
		now.UnixMilli(), now.UnixMilli()).Scan(&p.ID)
	if isUnique(err) {
		return Page{}, invalidf("another status page has the address %s", p.Slug)
	}
	if err != nil {
		return Page{}, err
	}
	if err := s.setSections(ctx, tx, p); err != nil {
		return Page{}, err
	}
	if err := tx.Commit(); err != nil {
		return Page{}, err
	}
	return s.PageByID(ctx, p.ID)
}

// UpdatePage saves a status page's settings and replaces its sections.
func (s *Store) UpdatePage(ctx context.Context, p Page, now time.Time) (Page, error) {
	if err := cleanPage(&p); err != nil {
		return Page{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	res, err := tx.ExecContext(ctx, s.q(`
		UPDATE status_pages SET slug = ?, title = ?, description = ?, footer = ?, published = ?, theme = ?, accent = ?,
			show_tags = ?, show_cert_expiry = ?, updated_at = ?
		WHERE id = ?`),
		p.Slug, p.Title, p.Description, p.Footer, p.Published, p.Theme, p.Accent, p.ShowTags, p.ShowCertExpiry,
		now.UnixMilli(), p.ID)
	if isUnique(err) {
		return Page{}, invalidf("another status page has the address %s", p.Slug)
	}
	if err != nil {
		return Page{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Page{}, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM status_page_sections WHERE page_id = ?`), p.ID); err != nil {
		return Page{}, err
	}
	if err := s.setSections(ctx, tx, p); err != nil {
		return Page{}, err
	}
	if err := tx.Commit(); err != nil {
		return Page{}, err
	}
	return s.PageByID(ctx, p.ID)
}

func (s *Store) setSections(ctx context.Context, tx *sql.Tx, p Page) error {
	for i, sec := range p.Sections {
		var id int64
		err := tx.QueryRowContext(ctx, s.q(`INSERT INTO status_page_sections (page_id, position, title, tag_id) VALUES (?, ?, ?, ?)
			RETURNING id`), p.ID, i, sec.Title, nullable(sec.TagID)).Scan(&id)
		if isForeignKey(err) {
			return invalidf("there is no tag %d", sec.TagID)
		}
		if err != nil {
			return err
		}
		seen := map[int64]bool{}
		for _, m := range sec.MonitorIDs {
			if seen[m] {
				continue
			}
			seen[m] = true
			_, err := tx.ExecContext(ctx, s.q(`INSERT INTO status_page_monitors (section_id, monitor_id) VALUES (?, ?)`), id, m)
			if isForeignKey(err) {
				return invalidf("there is no monitor %d", m)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// DeletePage deletes a status page with its logo and incidents.
func (s *Store) DeletePage(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM status_pages WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PageByID returns the status page with this ID, or ErrNotFound.
func (s *Store) PageByID(ctx context.Context, id int64) (Page, error) {
	return s.onePage(ctx, `WHERE p.id = ?`, id)
}

// PageBySlug returns the status page with this address, or ErrNotFound.
func (s *Store) PageBySlug(ctx context.Context, slug string) (Page, error) {
	return s.onePage(ctx, `WHERE p.slug = ?`, slug)
}

func (s *Store) onePage(ctx context.Context, where string, arg any) (Page, error) {
	ps, err := s.pages(ctx, where, arg)
	if err != nil {
		return Page{}, err
	}
	if len(ps) == 0 {
		return Page{}, ErrNotFound
	}
	return ps[0], nil
}

// Pages returns every status page, by title.
func (s *Store) Pages(ctx context.Context) ([]Page, error) { return s.pages(ctx, "") }

func (s *Store) pages(ctx context.Context, where string, args ...any) ([]Page, error) {
	out, err := s.pageRows(ctx, where, args...)
	if err != nil || len(out) == 0 {
		return out, err
	}
	byID := map[int64]*Page{}
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	secByID := map[int64]*Section{}
	rows, err := s.db.QueryContext(ctx, `SELECT id, page_id, title, COALESCE(tag_id, 0) FROM status_page_sections ORDER BY page_id, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sec Section
		var pageID int64
		if err := rows.Scan(&sec.ID, &pageID, &sec.Title, &sec.TagID); err != nil {
			return nil, err
		}
		if p := byID[pageID]; p != nil {
			sec.MonitorIDs = []int64{}
			p.Sections = append(p.Sections, sec)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, p := range byID {
		for i := range p.Sections {
			secByID[p.Sections[i].ID] = &p.Sections[i]
		}
	}
	err = s.eachPair(ctx, `SELECT section_id, monitor_id FROM status_page_monitors ORDER BY monitor_id`, func(sid, mid int64) {
		if sec := secByID[sid]; sec != nil {
			sec.MonitorIDs = append(sec.MonitorIDs, mid)
		}
	})
	return out, err
}

// pageRows reads the pages themselves, without their sections.
func (s *Store) pageRows(ctx context.Context, where string, args ...any) ([]Page, error) {
	// where is one of the fixed clauses above; values are bound arguments.
	// nosemgrep
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT p.id, p.slug, p.title, p.description, p.footer, p.published, p.theme, p.accent,
			p.show_tags, p.show_cert_expiry, COALESCE(l.updated_at, 0), p.created_at, p.updated_at
		FROM status_pages p LEFT JOIN status_page_logos l ON l.page_id = p.id `+where+` ORDER BY LOWER(p.title), p.id`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Page
	for rows.Next() {
		var p Page
		var logo, created, updated int64
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Description, &p.Footer, &p.Published, &p.Theme, &p.Accent,
			&p.ShowTags, &p.ShowCertExpiry, &logo, &created, &updated); err != nil {
			return nil, err
		}
		p.Sections = []Section{}
		p.LogoUpdated, p.CreatedAt, p.UpdatedAt = fromMillis(logo), fromMillis(created), fromMillis(updated)
		out = append(out, p)
	}
	return out, rows.Err()
}

// PublishedMonitors returns the IDs of the monitors on a published status
// page: named by a section, or with a section's tag.
func (s *Store) PublishedMonitors(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT spm.monitor_id FROM status_page_monitors spm
			JOIN status_page_sections sec ON sec.id = spm.section_id
			JOIN status_pages p ON p.id = sec.page_id WHERE p.published
		UNION
		SELECT mt.monitor_id FROM monitor_tags mt
			JOIN status_page_sections sec ON sec.tag_id = mt.tag_id
			JOIN status_pages p ON p.id = sec.page_id WHERE p.published`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// SetPageLogo stores a status page's logo, replacing any earlier one.
func (s *Store) SetPageLogo(ctx context.Context, pageID int64, contentType string, data []byte, now time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`
		INSERT INTO status_page_logos (page_id, content_type, data, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (page_id) DO UPDATE SET content_type = excluded.content_type, data = excluded.data,
			updated_at = excluded.updated_at`), pageID, contentType, data, now.UnixMilli())
	if isForeignKey(err) {
		return ErrNotFound
	}
	return err
}

// DeletePageLogo removes a status page's logo, if it has one.
func (s *Store) DeletePageLogo(ctx context.Context, pageID int64) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM status_page_logos WHERE page_id = ?`), pageID)
	return err
}

// PageLogo returns a status page's logo; ok is false if it has none.
func (s *Store) PageLogo(ctx context.Context, pageID int64) (p Picture, ok bool, err error) {
	var at int64
	err = s.db.QueryRowContext(ctx, s.q(`SELECT content_type, data, updated_at FROM status_page_logos WHERE page_id = ?`), pageID).
		Scan(&p.ContentType, &p.Data, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Picture{}, false, nil
	}
	if err != nil {
		return Picture{}, false, err
	}
	p.UpdatedAt = fromMillis(at)
	return p, true, nil
}
