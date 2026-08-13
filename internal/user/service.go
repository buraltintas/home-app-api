package user

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db     *pgxpool.Pool
	report *reporting.Service
}

func NewService(db *pgxpool.Pool, report *reporting.Service) *Service { return &Service{db, report} }

type PublicProfile struct {
	ID             uuid.UUID `json:"id"`
	Username       string    `json:"username"`
	DisplayName    string    `json:"display_name"`
	AvatarURL      string    `json:"avatar_url"`
	Bio            string    `json:"bio"`
	City           string    `json:"city"`
	FollowerCount  int       `json:"follower_count"`
	FollowingCount int       `json:"following_count"`
	PostCount      int       `json:"post_count"`
}
type Me struct {
	PublicProfile
	Email              string   `json:"email"`
	RelationshipStatus *string  `json:"relationship_status"`
	HasChildren        *bool    `json:"has_children"`
	ChildrenAgeRanges  []string `json:"children_age_ranges"`
	HousingStatus      *string  `json:"housing_status"`
	Occupation         *string  `json:"occupation"`
	AgeRange           *string  `json:"age_range"`
	HomeStyleInterests []string `json:"home_style_interests"`
	PreferredLocale    string   `json:"preferred_locale"`
}
type Update struct {
	Username           *string   `json:"username"`
	DisplayName        *string   `json:"display_name"`
	AvatarURL          *string   `json:"avatar_url"`
	Bio                *string   `json:"bio"`
	City               *string   `json:"city"`
	RelationshipStatus *string   `json:"relationship_status"`
	HasChildren        *bool     `json:"has_children"`
	ChildrenAgeRanges  *[]string `json:"children_age_ranges"`
	HousingStatus      *string   `json:"housing_status"`
	Occupation         *string   `json:"occupation"`
	AgeRange           *string   `json:"age_range"`
	HomeStyleInterests *[]string `json:"home_style_interests"`
	PreferredLocale    *string   `json:"preferred_locale"`
	BioLanguage        *string   `json:"bio_language"`
}

func (s *Service) Public(ctx context.Context, id uuid.UUID) (PublicProfile, error) {
	var p PublicProfile
	e := s.db.QueryRow(ctx, `SELECT u.id,coalesce(p.username::text,''),coalesce(p.display_name,''),coalesce(p.avatar_url,''),coalesce(p.bio,''),coalesce(p.city,''),(SELECT count(*) FROM follows WHERE following_id=u.id),(SELECT count(*) FROM follows WHERE follower_id=u.id),(SELECT count(*) FROM posts WHERE user_id=u.id AND deleted_at IS NULL) FROM users u JOIN user_profiles p ON p.user_id=u.id WHERE u.id=$1 AND u.deleted_at IS NULL`, id).Scan(&p.ID, &p.Username, &p.DisplayName, &p.AvatarURL, &p.Bio, &p.City, &p.FollowerCount, &p.FollowingCount, &p.PostCount)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, httpapi.E(404, "USER_NOT_FOUND", "User not found")
	}
	return p, e
}
func (s *Service) Me(ctx context.Context, id uuid.UUID) (Me, error) {
	var m Me
	e := s.db.QueryRow(ctx, `SELECT u.id,u.primary_email::text,coalesce(p.username::text,''),coalesce(p.display_name,''),coalesce(p.avatar_url,''),coalesce(p.bio,''),coalesce(p.city,''),(SELECT count(*) FROM follows WHERE following_id=u.id),(SELECT count(*) FROM follows WHERE follower_id=u.id),(SELECT count(*) FROM posts WHERE user_id=u.id AND deleted_at IS NULL),x.relationship_status,x.has_children,coalesce(x.children_age_ranges,'{}'),x.housing_status,x.occupation,x.age_range,coalesce(x.home_style_interests,'{}'),u.preferred_locale::text FROM users u JOIN user_profiles p ON p.user_id=u.id LEFT JOIN user_private_profiles x ON x.user_id=u.id WHERE u.id=$1 AND u.deleted_at IS NULL`, id).Scan(&m.ID, &m.Email, &m.Username, &m.DisplayName, &m.AvatarURL, &m.Bio, &m.City, &m.FollowerCount, &m.FollowingCount, &m.PostCount, &m.RelationshipStatus, &m.HasChildren, &m.ChildrenAgeRanges, &m.HousingStatus, &m.Occupation, &m.AgeRange, &m.HomeStyleInterests, &m.PreferredLocale)
	return m, e
}
func (s *Service) Update(ctx context.Context, id uuid.UUID, in Update) error {
	if e := validateUpdate(&in); e != nil {
		return e
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if in.PreferredLocale != nil {
		if _, e = tx.Exec(ctx, `UPDATE users SET preferred_locale=$2,updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, id, *in.PreferredLocale); e != nil {
			return e
		}
	}
	_, e = tx.Exec(ctx, `UPDATE user_profiles SET username=coalesce($2,username),display_name=coalesce($3,display_name),avatar_url=coalesce($4,avatar_url),bio=coalesce($5,bio),city=coalesce($6,city),bio_language=coalesce($7,bio_language),updated_at=now() WHERE user_id=$1`, id, in.Username, in.DisplayName, in.AvatarURL, in.Bio, in.City, in.BioLanguage)
	if e != nil {
		var pgErr *pgconn.PgError
		if errors.As(e, &pgErr) && pgErr.Code == "23505" {
			return httpapi.E(409, "USERNAME_TAKEN", "Username is already in use")
		}
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO user_private_profiles(user_id,relationship_status,has_children,children_age_ranges,housing_status,occupation,age_range,home_style_interests) VALUES($1,$2,$3,coalesce($4::text[],'{}'::text[]),$5,$6,$7,coalesce($8::text[],'{}'::text[])) ON CONFLICT(user_id) DO UPDATE SET relationship_status=coalesce($2,user_private_profiles.relationship_status),has_children=coalesce($3,user_private_profiles.has_children),children_age_ranges=coalesce($4::text[],user_private_profiles.children_age_ranges),housing_status=coalesce($5,user_private_profiles.housing_status),occupation=coalesce($6,user_private_profiles.occupation),age_range=coalesce($7,user_private_profiles.age_range),home_style_interests=coalesce($8::text[],user_private_profiles.home_style_interests),updated_at=now()`, id, in.RelationshipStatus, in.HasChildren, in.ChildrenAgeRanges, in.HousingStatus, in.Occupation, in.AgeRange, in.HomeStyleInterests)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func validateUpdate(in *Update) error {
	if in.Username != nil {
		v := strings.TrimSpace(*in.Username)
		if len(v) < 3 || len(v) > 30 {
			return httpapi.ErrInvalidInput
		}
		in.Username = &v
	}
	for _, field := range []struct {
		value *string
		max   int
	}{{in.DisplayName, 100}, {in.Bio, 500}, {in.City, 100}, {in.RelationshipStatus, 40}, {in.Occupation, 100}, {in.AgeRange, 40}} {
		if field.value != nil {
			v := strings.TrimSpace(*field.value)
			if len(v) > field.max || strings.ContainsRune(v, '\x00') {
				return httpapi.ErrInvalidInput
			}
			*field.value = v
		}
	}
	if in.AvatarURL != nil {
		v := strings.TrimSpace(*in.AvatarURL)
		if len(v) > 2048 {
			return httpapi.ErrInvalidInput
		}
		if v != "" {
			u, e := url.ParseRequestURI(v)
			if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return httpapi.ErrInvalidInput
			}
		}
		*in.AvatarURL = v
	}
	if in.HousingStatus != nil {
		allowed := map[string]bool{"owner": true, "renter": true, "living_with_family": true, "other": true}
		if !allowed[*in.HousingStatus] {
			return httpapi.ErrInvalidInput
		}
	}
	for _, field := range []*string{in.PreferredLocale, in.BioLanguage} {
		if field != nil {
			locale, ok := i18n.Normalize(*field)
			if !ok {
				return httpapi.ErrInvalidInput
			}
			*field = string(locale)
		}
	}
	for _, values := range []*[]string{in.ChildrenAgeRanges, in.HomeStyleInterests} {
		if values == nil {
			continue
		}
		if len(*values) > 20 {
			return httpapi.ErrInvalidInput
		}
		for i := range *values {
			(*values)[i] = strings.TrimSpace((*values)[i])
			if (*values)[i] == "" || len((*values)[i]) > 50 || strings.ContainsRune((*values)[i], '\x00') {
				return httpapi.ErrInvalidInput
			}
		}
	}
	return nil
}

type SearchHistory struct {
	ID          uuid.UUID `json:"id"`
	RawQuery    string    `json:"raw_query"`
	Intent      any       `json:"intent"`
	CreatedAt   time.Time `json:"created_at"`
	ResultCount int       `json:"result_count"`
}

func (s *Service) Searches(ctx context.Context, user uuid.UUID, limit int) ([]SearchHistory, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	rows, e := s.db.Query(ctx, `SELECT id,raw_query,parsed_intent,created_at,total_result_count FROM searches WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2`, user, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []SearchHistory
	for rows.Next() {
		var x SearchHistory
		if e = rows.Scan(&x.ID, &x.RawQuery, &x.Intent, &x.CreatedAt, &x.ResultCount); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) DeleteSearches(ctx context.Context, user uuid.UUID, id *uuid.UUID) error {
	if id == nil {
		_, e := s.db.Exec(ctx, `DELETE FROM searches WHERE user_id=$1`, user)
		if e == nil {
			e = s.report.RebuildSnapshot(ctx)
		}
		return e
	}
	tag, e := s.db.Exec(ctx, `DELETE FROM searches WHERE id=$1 AND user_id=$2`, *id, user)
	if e == nil && tag.RowsAffected() == 0 {
		return httpapi.E(404, "SEARCH_NOT_FOUND", "Search not found")
	}
	if e == nil {
		e = s.report.RebuildSnapshot(ctx)
	}
	return e
}

func (s *Service) DeleteAccount(ctx context.Context, user uuid.UUID) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, `SELECT DISTINCT store_id FROM posts WHERE user_id=$1 AND deleted_at IS NULL`, user)
	if e != nil {
		return e
	}
	var stores []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if e = rows.Scan(&id); e != nil {
			return e
		}
		stores = append(stores, id)
	}
	rows.Close()
	for _, query := range []string{
		`UPDATE posts SET deleted_at=now() WHERE user_id=$1 AND deleted_at IS NULL`,
		`UPDATE comments SET deleted_at=now() WHERE user_id=$1 AND deleted_at IS NULL`,
		`DELETE FROM likes WHERE user_id=$1`,
		`DELETE FROM follows WHERE follower_id=$1 OR following_id=$1`,
		`DELETE FROM favorites WHERE user_id=$1`,
		`DELETE FROM searches WHERE user_id=$1`,
		`DELETE FROM auth_identities WHERE user_id=$1`,
		`UPDATE auth_sessions SET revoked_at=coalesce(revoked_at,now()),revoke_reason='account_deleted' WHERE user_id=$1`,
		`DELETE FROM user_private_profiles WHERE user_id=$1`,
		`UPDATE user_profiles SET username=NULL,display_name='Deleted user',avatar_url=NULL,bio=NULL,city=NULL,updated_at=now() WHERE user_id=$1`,
	} {
		if _, e = tx.Exec(ctx, query, user); e != nil {
			return e
		}
	}
	tag, e := tx.Exec(ctx, `UPDATE users SET primary_email=('deleted+'||id::text||'@invalid.local')::citext,status='deleted',deleted_at=now(),updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, user)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return httpapi.E(404, "USER_NOT_FOUND", "User not found")
	}
	for _, store := range stores {
		if _, e = tx.Exec(ctx, `UPDATE store_stats ss SET rating_count=x.n,review_count=x.n,post_count=x.n,average_rating=x.avg,updated_at=now() FROM(SELECT count(*)::int n,coalesce(avg(rating),0) avg FROM posts WHERE store_id=$1 AND deleted_at IS NULL)x WHERE ss.store_id=$1`, store); e != nil {
			return e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	return s.report.RebuildSnapshot(ctx)
}
