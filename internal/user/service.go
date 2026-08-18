package user

import (
	"context"
	"errors"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

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

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func NewService(db *pgxpool.Pool, report *reporting.Service) *Service { return &Service{db, report} }

type PublicProfile struct {
	ID             uuid.UUID `json:"id"`
	Username       string    `json:"username"`
	DisplayName    string    `json:"display_name"`
	AvatarURL      string    `json:"avatar_url"`
	Bio            string    `json:"bio"`
	BioLanguage    string    `json:"bio_language,omitempty"`
	City           string    `json:"city"`
	FollowerCount  int       `json:"follower_count"`
	FollowingCount int       `json:"following_count"`
	PostCount      int       `json:"post_count"`
}
type Me struct {
	PublicProfile
	Email              string             `json:"email"`
	RelationshipStatus *string            `json:"relationship_status"`
	HasChildren        *bool              `json:"has_children"`
	ChildrenAgeRanges  []string           `json:"children_age_ranges"`
	HousingStatus      *string            `json:"housing_status"`
	Occupation         *string            `json:"occupation"`
	AgeRange           *string            `json:"age_range"`
	HomeStyleInterests []string           `json:"home_style_interests"`
	PreferredLocale    string             `json:"preferred_locale"`
	DiscoveryLocation  *DiscoveryLocation `json:"discovery_location"`
}

type DiscoveryLocation struct {
	Source         string    `json:"source"`
	Label          string    `json:"label"`
	Address        string    `json:"address"`
	PlaceID        string    `json:"place_id,omitempty"`
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	AccuracyMeters *float64  `json:"accuracy_meters,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type DiscoveryLocationInput struct {
	Source         string
	Label          string
	Address        string
	PlaceID        string
	Latitude       float64
	Longitude      float64
	AccuracyMeters *float64
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
	e := s.db.QueryRow(ctx, `SELECT u.id,coalesce(p.username::text,''),coalesce(p.display_name,''),coalesce(p.avatar_url,''),coalesce(p.bio,''),coalesce(p.bio_language::text,''),coalesce(p.city,''),(SELECT count(*) FROM follows WHERE following_id=u.id),(SELECT count(*) FROM follows WHERE follower_id=u.id),(SELECT count(*) FROM posts WHERE user_id=u.id AND deleted_at IS NULL) FROM users u JOIN user_profiles p ON p.user_id=u.id WHERE u.id=$1 AND u.deleted_at IS NULL`, id).Scan(&p.ID, &p.Username, &p.DisplayName, &p.AvatarURL, &p.Bio, &p.BioLanguage, &p.City, &p.FollowerCount, &p.FollowingCount, &p.PostCount)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, httpapi.E(404, "USER_NOT_FOUND", "User not found")
	}
	return p, e
}
func (s *Service) Me(ctx context.Context, id uuid.UUID) (Me, error) {
	var m Me
	var locationSource, locationLabel, locationAddress, locationPlaceID *string
	var latitude, longitude, accuracy *float64
	var locationUpdatedAt *time.Time
	e := s.db.QueryRow(ctx, `SELECT u.id,u.primary_email::text,coalesce(p.username::text,''),coalesce(p.display_name,''),coalesce(p.avatar_url,''),coalesce(p.bio,''),coalesce(p.bio_language::text,''),coalesce(p.city,''),(SELECT count(*) FROM follows WHERE following_id=u.id),(SELECT count(*) FROM follows WHERE follower_id=u.id),(SELECT count(*) FROM posts WHERE user_id=u.id AND deleted_at IS NULL),x.relationship_status,x.has_children,coalesce(x.children_age_ranges,'{}'),x.housing_status,x.occupation,x.age_range,coalesce(x.home_style_interests,'{}'),u.preferred_locale::text,x.discovery_location_source,x.discovery_location_label,x.discovery_location_address,x.discovery_location_place_id,ST_Y(x.discovery_location::geometry),ST_X(x.discovery_location::geometry),x.discovery_location_accuracy_meters::double precision,x.discovery_location_updated_at FROM users u JOIN user_profiles p ON p.user_id=u.id LEFT JOIN user_private_profiles x ON x.user_id=u.id WHERE u.id=$1 AND u.deleted_at IS NULL`, id).Scan(&m.ID, &m.Email, &m.Username, &m.DisplayName, &m.AvatarURL, &m.Bio, &m.BioLanguage, &m.City, &m.FollowerCount, &m.FollowingCount, &m.PostCount, &m.RelationshipStatus, &m.HasChildren, &m.ChildrenAgeRanges, &m.HousingStatus, &m.Occupation, &m.AgeRange, &m.HomeStyleInterests, &m.PreferredLocale, &locationSource, &locationLabel, &locationAddress, &locationPlaceID, &latitude, &longitude, &accuracy, &locationUpdatedAt)
	if e == nil && locationSource != nil && latitude != nil && longitude != nil && locationUpdatedAt != nil {
		m.DiscoveryLocation = &DiscoveryLocation{Source: *locationSource, Label: derefString(locationLabel), Address: derefString(locationAddress), PlaceID: derefString(locationPlaceID), Latitude: *latitude, Longitude: *longitude, AccuracyMeters: accuracy, UpdatedAt: *locationUpdatedAt}
	}
	return m, e
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) SetDiscoveryLocation(ctx context.Context, id uuid.UUID, in DiscoveryLocationInput) error {
	if err := validateDiscoveryLocation(&in); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `INSERT INTO user_private_profiles(user_id,discovery_location,discovery_location_source,discovery_location_label,discovery_location_address,discovery_location_place_id,discovery_location_accuracy_meters,discovery_location_updated_at)
		VALUES($1,ST_SetSRID(ST_MakePoint($6,$5),4326)::geography,$2,nullif($3,''),nullif($4,''),nullif($7,''),$8,now())
		ON CONFLICT(user_id) DO UPDATE SET discovery_location=excluded.discovery_location,discovery_location_source=excluded.discovery_location_source,discovery_location_label=excluded.discovery_location_label,discovery_location_address=excluded.discovery_location_address,discovery_location_place_id=excluded.discovery_location_place_id,discovery_location_accuracy_meters=excluded.discovery_location_accuracy_meters,discovery_location_updated_at=now(),updated_at=now()`, id, in.Source, in.Label, in.Address, in.Latitude, in.Longitude, in.PlaceID, in.AccuracyMeters)
	return err
}

func (s *Service) ClearDiscoveryLocation(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE user_private_profiles SET discovery_location=NULL,discovery_location_source=NULL,discovery_location_label=NULL,discovery_location_address=NULL,discovery_location_place_id=NULL,discovery_location_accuracy_meters=NULL,discovery_location_updated_at=NULL,updated_at=now() WHERE user_id=$1`, id)
	return err
}

func validateDiscoveryLocation(in *DiscoveryLocationInput) error {
	in.Source = strings.TrimSpace(in.Source)
	in.Label = strings.TrimSpace(in.Label)
	in.Address = strings.TrimSpace(in.Address)
	in.PlaceID = strings.TrimSpace(in.PlaceID)
	if (in.Source != "device" && in.Source != "manual") || math.IsNaN(in.Latitude) || math.IsNaN(in.Longitude) || math.IsInf(in.Latitude, 0) || math.IsInf(in.Longitude, 0) || in.Latitude < -90 || in.Latitude > 90 || in.Longitude < -180 || in.Longitude > 180 {
		return httpapi.ErrInvalidInput
	}
	if utf8.RuneCountInString(in.Label) > 200 || utf8.RuneCountInString(in.Address) > 500 || utf8.RuneCountInString(in.PlaceID) > 300 {
		return httpapi.ErrInvalidInput
	}
	if in.Source == "manual" {
		if in.PlaceID == "" || in.Label == "" || in.AccuracyMeters != nil {
			return httpapi.ErrInvalidInput
		}
		return nil
	}
	if in.PlaceID != "" || in.Label != "" || in.Address != "" || in.AccuracyMeters == nil || *in.AccuracyMeters <= 0 || *in.AccuracyMeters > 1000 || math.IsNaN(*in.AccuracyMeters) || math.IsInf(*in.AccuracyMeters, 0) {
		return httpapi.ErrInvalidInput
	}
	return nil
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
		if utf8.RuneCountInString(v) < 3 || utf8.RuneCountInString(v) > 30 || !usernamePattern.MatchString(v) {
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
			if utf8.RuneCountInString(v) > field.max || strings.ContainsRune(v, '\x00') {
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
			if (*values)[i] == "" || utf8.RuneCountInString((*values)[i]) > 50 || strings.ContainsRune((*values)[i], '\x00') {
				return httpapi.ErrInvalidInput
			}
		}
	}
	return nil
}

type SearchHistory struct {
	ID          uuid.UUID             `json:"id"`
	RawQuery    string                `json:"raw_query"`
	Intent      any                   `json:"intent"`
	CreatedAt   time.Time             `json:"created_at"`
	ResultCount int                   `json:"result_count"`
	Results     []SearchHistoryResult `json:"results"`
}

type SearchHistoryResult struct {
	StoreID        uuid.UUID `json:"store_id"`
	Name           string    `json:"name"`
	Address        string    `json:"address"`
	City           string    `json:"city"`
	District       string    `json:"district"`
	Rank           int       `json:"rank"`
	DistanceMeters *int      `json:"distance_meters,omitempty"`
	Source         string    `json:"source"`
}

func (s *Service) Searches(ctx context.Context, user uuid.UUID, limit int) ([]SearchHistory, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	// Searching the same thing twice is normal behaviour, not two pieces of history.
	// Every search is still recorded for attribution; the list shows the latest of each
	// distinct query so the same words never stack up three times in a row.
	rows, e := s.db.Query(ctx, `SELECT id,raw_query,parsed_intent,created_at,total_result_count FROM (SELECT DISTINCT ON (lower(coalesce(nullif(normalized_query,''),raw_query))) id,raw_query,parsed_intent,created_at,total_result_count FROM searches WHERE user_id=$1 AND status='completed' ORDER BY lower(coalesce(nullif(normalized_query,''),raw_query)),created_at DESC) recent ORDER BY created_at DESC LIMIT $2`, user, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []SearchHistory{}
	index := map[uuid.UUID]int{}
	ids := make([]uuid.UUID, 0, limit)
	for rows.Next() {
		var x SearchHistory
		if e = rows.Scan(&x.ID, &x.RawQuery, &x.Intent, &x.CreatedAt, &x.ResultCount); e != nil {
			return nil, e
		}
		x.Results = []SearchHistoryResult{}
		index[x.ID] = len(out)
		ids = append(ids, x.ID)
		out = append(out, x)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if len(ids) == 0 {
		return out, nil
	}
	// Stored results let history render straight from our own data, with no
	// provider round-trip. This relies on search_results.store_id being populated.
	resultRows, e := s.db.Query(ctx, `SELECT r.search_id,r.store_id,st.name,coalesce(st.address,''),st.city,coalesce(st.district,''),r.rank,r.distance_meters,r.source FROM search_results r JOIN stores st ON st.id=r.store_id AND st.deleted_at IS NULL WHERE r.search_id=ANY($1) AND r.rank<=5 ORDER BY r.search_id,r.rank`, ids)
	if e != nil {
		return nil, e
	}
	defer resultRows.Close()
	for resultRows.Next() {
		var searchID uuid.UUID
		var x SearchHistoryResult
		if e = resultRows.Scan(&searchID, &x.StoreID, &x.Name, &x.Address, &x.City, &x.District, &x.Rank, &x.DistanceMeters, &x.Source); e != nil {
			return nil, e
		}
		if i, ok := index[searchID]; ok {
			out[i].Results = append(out[i].Results, x)
		}
	}
	return out, resultRows.Err()
}
func (s *Service) DeleteSearches(ctx context.Context, user uuid.UUID, id *uuid.UUID) error {
	if id == nil {
		_, e := s.db.Exec(ctx, `DELETE FROM searches WHERE user_id=$1`, user)
		if e == nil {
			e = s.report.RebuildSnapshot(ctx)
		}
		return e
	}
	// The list collapses repeats, so removing an entry has to remove every search that
	// produced it. Otherwise deleting one makes an older identical row reappear.
	tag, e := s.db.Exec(ctx, `DELETE FROM searches WHERE user_id=$2 AND lower(coalesce(nullif(normalized_query,''),raw_query))=(SELECT lower(coalesce(nullif(normalized_query,''),raw_query)) FROM searches WHERE id=$1 AND user_id=$2)`, *id, user)
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
	var email string
	if e = tx.QueryRow(ctx, `SELECT primary_email::text FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL FOR UPDATE`, user).Scan(&email); errors.Is(e, pgx.ErrNoRows) {
		return httpapi.E(404, "USER_NOT_FOUND", "User not found")
	} else if e != nil {
		return e
	}
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
		`UPDATE posts SET body='',content_language=NULL,deleted_at=now(),updated_at=now() WHERE user_id=$1 AND deleted_at IS NULL`,
		`UPDATE comments SET body='',content_language=NULL,deleted_at=now(),updated_at=now() WHERE user_id=$1 AND deleted_at IS NULL`,
		`DELETE FROM likes WHERE user_id=$1`,
		`DELETE FROM follows WHERE follower_id=$1 OR following_id=$1`,
		`DELETE FROM favorites WHERE user_id=$1`,
		`DELETE FROM searches WHERE user_id=$1`,
		`DELETE FROM store_visit_verifications WHERE user_id=$1`,
		`UPDATE media SET status='deleted' WHERE owner_user_id=$1 AND status<>'deleted'`,
		`DELETE FROM push_devices WHERE user_id=$1`,
		`DELETE FROM notification_preferences WHERE user_id=$1`,
		`DELETE FROM notification_outbox WHERE user_id=$1`,
		`UPDATE visitor_sessions SET linked_user_id=NULL WHERE linked_user_id=$1`,
		`UPDATE platform_events SET user_id=NULL WHERE user_id=$1`,
		`UPDATE auth_sessions SET revoked_at=coalesce(revoked_at,now()),revoke_reason='account_deactivated' WHERE user_id=$1`,
		`DELETE FROM user_private_profiles WHERE user_id=$1`,
		`UPDATE user_profiles SET username=NULL,display_name='Deleted user',avatar_url=NULL,bio=NULL,bio_language=NULL,city=NULL,updated_at=now() WHERE user_id=$1`,
	} {
		if _, e = tx.Exec(ctx, query, user); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, `DELETE FROM email_verification_codes WHERE normalized_email=$1`, email); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM email_outbox WHERE recipient=$1`, email); e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `UPDATE users SET status='inactive',preferred_locale='tr',deleted_at=now(),updated_at=now() WHERE id=$1 AND status='active' AND deleted_at IS NULL`, user)
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
