package user

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) *Service { return &Service{db} }

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
	e := s.db.QueryRow(ctx, `SELECT u.id,u.primary_email::text,coalesce(p.username::text,''),coalesce(p.display_name,''),coalesce(p.avatar_url,''),coalesce(p.bio,''),coalesce(p.city,''),(SELECT count(*) FROM follows WHERE following_id=u.id),(SELECT count(*) FROM follows WHERE follower_id=u.id),(SELECT count(*) FROM posts WHERE user_id=u.id AND deleted_at IS NULL),x.relationship_status,x.has_children,coalesce(x.children_age_ranges,'{}'),x.housing_status,x.occupation,x.age_range,coalesce(x.home_style_interests,'{}') FROM users u JOIN user_profiles p ON p.user_id=u.id LEFT JOIN user_private_profiles x ON x.user_id=u.id WHERE u.id=$1 AND u.deleted_at IS NULL`, id).Scan(&m.ID, &m.Email, &m.Username, &m.DisplayName, &m.AvatarURL, &m.Bio, &m.City, &m.FollowerCount, &m.FollowingCount, &m.PostCount, &m.RelationshipStatus, &m.HasChildren, &m.ChildrenAgeRanges, &m.HousingStatus, &m.Occupation, &m.AgeRange, &m.HomeStyleInterests)
	return m, e
}
func (s *Service) Update(ctx context.Context, id uuid.UUID, in Update) error {
	if in.Username != nil {
		v := strings.TrimSpace(*in.Username)
		if len(v) < 3 || len(v) > 30 {
			return httpapi.ErrInvalidInput
		}
		in.Username = &v
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, `UPDATE user_profiles SET username=coalesce($2,username),display_name=coalesce($3,display_name),avatar_url=coalesce($4,avatar_url),bio=coalesce($5,bio),city=coalesce($6,city),updated_at=now() WHERE user_id=$1`, id, in.Username, in.DisplayName, in.AvatarURL, in.Bio, in.City)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO user_private_profiles(user_id,relationship_status,has_children,children_age_ranges,housing_status,occupation,age_range,home_style_interests) VALUES($1,$2,$3,coalesce($4,'{}'),$5,$6,$7,coalesce($8,'{}')) ON CONFLICT(user_id) DO UPDATE SET relationship_status=coalesce($2,user_private_profiles.relationship_status),has_children=coalesce($3,user_private_profiles.has_children),children_age_ranges=coalesce($4,user_private_profiles.children_age_ranges),housing_status=coalesce($5,user_private_profiles.housing_status),occupation=coalesce($6,user_private_profiles.occupation),age_range=coalesce($7,user_private_profiles.age_range),home_style_interests=coalesce($8,user_private_profiles.home_style_interests),updated_at=now()`, id, in.RelationshipStatus, in.HasChildren, in.ChildrenAgeRanges, in.HousingStatus, in.Occupation, in.AgeRange, in.HomeStyleInterests)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
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
		return e
	}
	tag, e := s.db.Exec(ctx, `DELETE FROM searches WHERE id=$1 AND user_id=$2`, *id, user)
	if e == nil && tag.RowsAffected() == 0 {
		return httpapi.E(404, "SEARCH_NOT_FOUND", "Search not found")
	}
	return e
}
