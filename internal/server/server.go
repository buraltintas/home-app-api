package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/burakaltintas/home-app-api/internal/auth"
	"github.com/burakaltintas/home-app-api/internal/brand"
	. "github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/media"
	appmw "github.com/burakaltintas/home-app-api/internal/middleware"
	"github.com/burakaltintas/home-app-api/internal/observability"
	searchpkg "github.com/burakaltintas/home-app-api/internal/search"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/burakaltintas/home-app-api/internal/social"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	userpkg "github.com/burakaltintas/home-app-api/internal/user"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Server struct {
	db      *pgxpool.Pool
	auth    *auth.Service
	stores  *storepkg.Service
	social  *social.Service
	search  *searchpkg.Service
	users   *userpkg.Service
	media   *media.Service
	hashKey []byte
}

func NewServer(db *pgxpool.Pool, a *auth.Service, st *storepkg.Service, so *social.Service, se *searchpkg.Service, u *userpkg.Service, m *media.Service, hashKey []byte) *Server {
	return &Server{db, a, st, so, se, u, m, hashKey}
}

func (s *Server) Router(log *slog.Logger, bff []string, tokens *security.TokenManager, options ...any) http.Handler {
	metricsToken := ""
	defaultLocale := i18n.DefaultLocale
	for _, option := range options {
		switch value := option.(type) {
		case string:
			metricsToken = value
		case i18n.Locale:
			defaultLocale = value
		}
	}
	r := chi.NewRouter()
	r.Use(appmw.RequestID, appmw.Recover(log), otelhttp.NewMiddleware(brand.ServiceName), observability.HTTPMiddleware, appmw.Logging(log), appmw.SecurityHeaders, appmw.RequestLocale(defaultLocale))
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) { JSON(w, 200, map[string]string{"status": "ok"}) })
	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if e := s.db.Ping(ctx); e != nil {
			JSON(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		JSON(w, 200, map[string]string{"status": "ready"})
	})
	r.Handle("/metrics", observability.MetricsHandler(metricsToken))
	r.Get("/media/{id}", s.publicMedia)
	if uploads := s.media.UploadHandler(); uploads != nil {
		r.Mount("/uploads", http.StripPrefix("/uploads", uploads))
	}
	r.Route("/v1", func(r chi.Router) {
		r.Use(appmw.BFF(bff))
		r.Use(appmw.NewLimiter(180, 40).Middleware)
		r.Use(appmw.OptionalAuth(tokens))
		r.Use(appmw.ActiveAccount(s.db))
		r.Use(appmw.UserLocale(s.db))
		searchLimit := appmw.NewLimiter(30, 8)
		writeLimit := appmw.NewLimiter(20, 6)
		socialLimit := appmw.NewLimiter(60, 15)
		r.Get("/feed", s.feed)
		r.With(searchLimit.Middleware).Post("/search", s.searchStores)
		r.With(searchLimit.Middleware).Get("/locations/search", s.searchLocations)
		r.With(searchLimit.Middleware).Get("/places/photo", s.placePhoto)
		r.Get("/stores/index", s.storeIndex)
		r.Get("/stores/search", s.storeSearch)
		r.Get("/stores/nearby", s.storeSearch)
		r.Get("/stores/{id}", s.storeDetail)
		r.Get("/stores/{id}/posts", s.postsByStore)
		r.With(appmw.RequireAuth, writeLimit.Middleware).Post("/stores/resolve-external", s.resolveExternalStore)
		r.Get("/posts/{id}", s.postDetail)
		r.Get("/posts/{id}/comments", s.comments)
		r.Get("/users/{id}", s.userPublic)
		r.Get("/users/{id}/posts", s.postsByUser)
		r.Route("/auth", func(r chi.Router) {
			r.Use(appmw.NewLimiter(30, 8).Middleware)
			r.Post("/email/request-code", s.requestCode)
			r.Post("/email/verify-code", s.verifyCode)
			r.Post("/google", s.google)
			r.Post("/refresh", s.refresh)
			r.With(appmw.RequireAuth).Post("/logout", s.logout)
			r.With(appmw.RequireAuth).Post("/logout-all", s.logoutAll)
		})
		r.Group(func(r chi.Router) {
			r.Use(appmw.RequireAuth)
			r.Get("/me", s.me)
			r.Delete("/me", s.deleteAccount)
			r.Patch("/me", s.updateMe)
			r.With(writeLimit.Middleware).Put("/me/discovery-location", s.updateDiscoveryLocation)
			r.Delete("/me/discovery-location", s.clearDiscoveryLocation)
			r.Get("/me/favorites", s.myFavorites)
			r.Get("/me/searches", s.mySearches)
			r.Delete("/me/searches", s.deleteMySearches)
			r.Delete("/me/searches/{id}", s.deleteMySearch)
			r.With(writeLimit.Middleware).Post("/posts", s.createPost)
			r.With(writeLimit.Middleware).Post("/stores/{id}/visit-verifications", s.verifyStoreVisit)
			r.With(writeLimit.Middleware).Post("/media/uploads", s.createMediaUpload)
			r.With(writeLimit.Middleware).Post("/media/{id}/complete", s.completeMediaUpload)
			r.Delete("/posts/{id}", s.deletePost)
			r.With(socialLimit.Middleware).Post("/posts/{id}/like", s.like)
			r.Delete("/posts/{id}/like", s.unlike)
			r.With(socialLimit.Middleware).Post("/posts/{id}/comments", s.addComment)
			r.Delete("/comments/{id}", s.deleteComment)
			r.With(socialLimit.Middleware).Post("/users/{id}/follow", s.follow)
			r.Delete("/users/{id}/follow", s.unfollow)
			r.With(socialLimit.Middleware).Post("/stores/{id}/favorite", s.favorite)
			r.Delete("/stores/{id}/favorite", s.unfavorite)
		})
		r.Post("/searches/{id}/interactions", s.interaction)
	})
	return r
}
func (s *Server) publicMedia(w http.ResponseWriter, r *http.Request) {
	id, e := parseID(r)
	if e == nil {
		var target string
		target, e = s.media.PublicURL(r.Context(), id)
		if e == nil {
			w.Header().Set("Cache-Control", "private, max-age=300")
			http.Redirect(w, r, target, http.StatusTemporaryRedirect)
			return
		}
	}
	WriteError(w, e, r.Context())
}

// placePhoto streams a Google place photo. The photo name is validated against a
// strict pattern before it reaches a provider URL, and bytes are never persisted.
func (s *Server) placePhoto(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !searchpkg.ValidPhotoName(name) {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	width, _ := strconv.Atoi(r.URL.Query().Get("max_width"))
	if width == 0 {
		width = 520
	}
	body, contentType, e := s.search.PlacePhoto(r.Context(), name, width)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	defer body.Close()
	if contentType == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(200)
	_, _ = io.Copy(w, io.LimitReader(body, 8<<20))
}

func (s *Server) createMediaUpload(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	var in media.CreateRequest
	if e := Decode(w, r, &in, 16<<10); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	x, e := s.media.Create(r.Context(), p.UserID, in)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 201, x)
}
func (s *Server) completeMediaUpload(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	id, e := parseID(r)
	var in struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if e == nil {
		e = Decode(w, r, &in, 16<<10)
	}
	if e == nil {
		e = s.media.Complete(r.Context(), p.UserID, id, in.Width, in.Height)
	}
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) requestCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if e := Decode(w, r, &in, 16<<10); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var visitor *uuid.UUID
	if v, ok := appmw.VisitorID(r); ok {
		visitor = &v
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if e := s.auth.RequestCode(r.Context(), in.Email, visitor, auth.IPHash(s.hashKey, ip)); e != nil {
		observability.Auth("otp_request", "failure")
		WriteError(w, e, r.Context())
		return
	}
	observability.Auth("otp_request", "success")
	JSON(w, 202, map[string]string{"status": "accepted"})
}
func client(r *http.Request) auth.Client {
	return auth.Client{Type: strings.TrimSpace(r.Header.Get("X-Client-Type")), Metadata: map[string]any{"version": strings.TrimSpace(r.Header.Get("X-Client-Version"))}}
}
func (s *Server) verifyCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if e := Decode(w, r, &in, 16<<10); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	x, e := s.auth.VerifyCode(r.Context(), in.Email, in.Code, client(r))
	if e != nil {
		observability.Auth("otp_verify", "failure")
		WriteError(w, e, r.Context())
		return
	}
	observability.Auth("otp_verify", "success")
	if visitor, ok := appmw.VisitorID(r); ok {
		_ = s.auth.LinkVisitor(r.Context(), x.UserID, visitor)
	}
	JSON(w, 200, x)
}
func (s *Server) google(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDToken string `json:"id_token"`
	}
	if e := Decode(w, r, &in, 64<<10); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	x, e := s.auth.Google(r.Context(), in.IDToken, client(r))
	if e != nil {
		observability.Auth("google_login", "failure")
		WriteError(w, e, r.Context())
		return
	}
	observability.Auth("google_login", "success")
	if visitor, ok := appmw.VisitorID(r); ok {
		_ = s.auth.LinkVisitor(r.Context(), x.UserID, visitor)
	}
	JSON(w, 200, x)
}
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if e := Decode(w, r, &in, 16<<10); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	x, e := s.auth.Refresh(r.Context(), in.RefreshToken, client(r))
	if e != nil {
		observability.Auth("refresh", "failure")
		WriteError(w, e, r.Context())
		return
	}
	observability.Auth("refresh", "success")
	JSON(w, 200, x)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	if e := s.auth.Logout(r.Context(), p.UserID, p.SessionID, false); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	if e := s.auth.Logout(r.Context(), p.UserID, p.SessionID, true); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(204)
}

func viewer(r *http.Request) *uuid.UUID {
	if p, ok := appmw.PrincipalFrom(r.Context()); ok {
		return &p.UserID
	}
	return nil
}

// A store may be addressed by id or by its slug, so that shared links can carry the
// store's name instead of a uuid. Slugs are unique, so the two never collide.
func parseStoreRef(r *http.Request, s *Server) (uuid.UUID, error) {
	raw := chi.URLParam(r, "id")
	if id, e := uuid.Parse(raw); e == nil {
		return id, nil
	}
	return s.stores.ResolveSlug(r.Context(), raw)
}

func parseID(r *http.Request) (uuid.UUID, error) {
	id, e := uuid.Parse(chi.URLParam(r, "id"))
	if e != nil {
		return uuid.Nil, ErrInvalidInput
	}
	return id, nil
}
func queryFloat(r *http.Request, key string) (*float64, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil, nil
	}
	n, e := strconv.ParseFloat(v, 64)
	if e != nil {
		return nil, ErrInvalidInput
	}
	return &n, nil
}
func queryInt(r *http.Request, key string, d int) int {
	n, e := strconv.Atoi(r.URL.Query().Get(key))
	if e != nil {
		return d
	}
	return n
}

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	lat, e := queryFloat(r, "latitude")
	lon, lonErr := queryFloat(r, "longitude")
	if e != nil || lonErr != nil || (lat == nil) != (lon == nil) || (lat != nil && !storepkg.ValidCoordinates(*lat, *lon)) {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	items, next, e := s.social.Feed(r.Context(), viewer(r), r.URL.Query().Get("cursor"), queryInt(r, "limit", 20), social.FeedContext{Latitude: lat, Longitude: lon})
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) searchLocations(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 5)
	items, e := s.search.ResolveLocations(r.Context(), r.URL.Query().Get("q"), limit)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) storeSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	lat, e := queryFloat(r, "latitude")
	lon, lonErr := queryFloat(r, "longitude")
	radius := queryInt(r, "radius_meters", 10000)
	if e != nil || lonErr != nil || (lat == nil) != (lon == nil) || (lat != nil && !storepkg.ValidCoordinates(*lat, *lon)) || radius < 100 || radius > 50000 || utf8.RuneCountInString(r.URL.Query().Get("q")) > 500 {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	items, e := s.stores.Search(r.Context(), r.URL.Query().Get("q"), nil, "", lat, lon, radius, queryInt(r, "limit", 20), viewer(r))
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var visitor *uuid.UUID
	if id, ok := appmw.VisitorID(r); ok {
		visitor = &id
	}
	searchID, visitor, e := s.search.RecordInternalSearch(r.Context(), viewer(r), visitor, searchpkg.Request{Query: r.URL.Query().Get("q"), Latitude: lat, Longitude: lon, RadiusMeters: radius}, items, time.Since(start))
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"search_id": searchID, "visitor_session_id": visitor, "items": items})
}

// storeIndex enumerates stores for sitemap generation. Search is query driven and can
// never answer "every store you have", which left the sitemap listing no stores at all.
func (s *Server) storeIndex(w http.ResponseWriter, r *http.Request) {
	items, e := s.stores.Index(r.Context(), queryInt(r, "offset", 0), queryInt(r, "limit", 1000))
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) storeDetail(w http.ResponseWriter, r *http.Request) {
	id, e := parseStoreRef(r, s)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	lat, latErr := queryFloat(r, "latitude")
	lon, lonErr := queryFloat(r, "longitude")
	if latErr != nil || lonErr != nil || (lat == nil) != (lon == nil) || (lat != nil && !storepkg.ValidCoordinates(*lat, *lon)) {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	x, e := s.stores.Get(r.Context(), id, viewer(r), lat, lon)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	posts, e := s.social.PostsBy(r.Context(), "store_id", id, viewer(r), 5)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"store": x, "recent_posts": posts})
}
func (s *Server) postDetail(w http.ResponseWriter, r *http.Request) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	x, e := s.social.GetPost(r.Context(), id, viewer(r))
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, x)
}
func (s *Server) comments(w http.ResponseWriter, r *http.Request) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	x, e := s.social.Comments(r.Context(), id, queryInt(r, "limit", 50))
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": x})
}
func (s *Server) postsByStore(w http.ResponseWriter, r *http.Request) { s.postsBy(w, r, "store_id") }
func (s *Server) postsByUser(w http.ResponseWriter, r *http.Request)  { s.postsBy(w, r, "user_id") }
func (s *Server) postsBy(w http.ResponseWriter, r *http.Request, column string) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	x, e := s.social.PostsBy(r.Context(), column, id, viewer(r), queryInt(r, "limit", 20))
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": x})
}
func (s *Server) userPublic(w http.ResponseWriter, r *http.Request) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	x, e := s.users.Public(r.Context(), id)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, x)
}

func (s *Server) searchStores(w http.ResponseWriter, r *http.Request) {
	var in searchpkg.Request
	if e := Decode(w, r, &in, 64<<10); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var user *uuid.UUID
	if p, ok := appmw.PrincipalFrom(r.Context()); ok {
		user = &p.UserID
	}
	var visitor *uuid.UUID
	if v, ok := appmw.VisitorID(r); ok {
		visitor = &v
	}
	x, e := s.search.Search(r.Context(), user, visitor, in)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, x)
}
func (s *Server) resolveExternalStore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider string `json:"provider"`
		PlaceID  string `json:"place_id"`
	}
	if e := Decode(w, r, &in, 16<<10); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	if in.Provider != "google" {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	id, e := s.search.MaterializeGoogleStore(r.Context(), in.PlaceID)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"id": id})
}
func (s *Server) interaction(w http.ResponseWriter, r *http.Request) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var in struct {
		SearchResultID *uuid.UUID `json:"search_result_id"`
		EventType      string     `json:"event_type"`
		IdempotencyKey string     `json:"idempotency_key"`
	}
	if e = Decode(w, r, &in, 16<<10); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var user *uuid.UUID
	if p, ok := appmw.PrincipalFrom(r.Context()); ok {
		user = &p.UserID
	}
	var vis *uuid.UUID
	if v, ok := appmw.VisitorID(r); ok {
		vis = &v
	}
	if e = s.search.Interaction(r.Context(), id, user, vis, in.SearchResultID, in.EventType, in.IdempotencyKey); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	x, e := s.users.Me(r.Context(), p.UserID)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, x)
}
func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	var in userpkg.Update
	if e := Decode(w, r, &in, 64<<10); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	if e := s.users.Update(r.Context(), p.UserID, in); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	s.me(w, r)
}

type discoveryLocationRequest struct {
	Source         string   `json:"source"`
	PlaceID        string   `json:"place_id"`
	Latitude       *float64 `json:"latitude"`
	Longitude      *float64 `json:"longitude"`
	AccuracyMeters *float64 `json:"accuracy_meters"`
}

func (s *Server) updateDiscoveryLocation(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	var request discoveryLocationRequest
	if err := Decode(w, r, &request, 16<<10); err != nil {
		WriteError(w, err, r.Context())
		return
	}
	input := userpkg.DiscoveryLocationInput{Source: strings.TrimSpace(request.Source), AccuracyMeters: request.AccuracyMeters}
	switch input.Source {
	case "manual":
		if request.Latitude != nil || request.Longitude != nil || request.AccuracyMeters != nil {
			WriteError(w, ErrInvalidInput, r.Context())
			return
		}
		location, err := s.search.ResolveLocationPlace(r.Context(), request.PlaceID)
		if err != nil {
			WriteError(w, err, r.Context())
			return
		}
		input.Label, input.Address, input.PlaceID = location.Name, location.Address, location.PlaceID
		input.Latitude, input.Longitude = location.Latitude, location.Longitude
	case "device":
		if request.PlaceID != "" || request.Latitude == nil || request.Longitude == nil || request.AccuracyMeters == nil {
			WriteError(w, ErrInvalidInput, r.Context())
			return
		}
		input.Latitude, input.Longitude = *request.Latitude, *request.Longitude
	default:
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	if err := s.users.SetDiscoveryLocation(r.Context(), p.UserID, input); err != nil {
		WriteError(w, err, r.Context())
		return
	}
	s.me(w, r)
}

func (s *Server) clearDiscoveryLocation(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	if err := s.users.ClearDiscoveryLocation(r.Context(), p.UserID); err != nil {
		WriteError(w, err, r.Context())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	if e := s.users.DeleteAccount(r.Context(), p.UserID); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) myFavorites(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	x, e := s.stores.Favorites(r.Context(), p.UserID, queryInt(r, "limit", 50))
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": x})
}
func (s *Server) mySearches(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	x, e := s.users.Searches(r.Context(), p.UserID, queryInt(r, "limit", 30))
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": x})
}
func (s *Server) deleteMySearches(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	if e := s.users.DeleteSearches(r.Context(), p.UserID, nil); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) deleteMySearch(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	id, e := parseID(r)
	if e == nil {
		e = s.users.DeleteSearches(r.Context(), p.UserID, &id)
	}
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) createPost(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	var in social.CreatePost
	if e := Decode(w, r, &in, 128<<10); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	id, e := s.social.CreatePost(r.Context(), p.UserID, in)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	if in.OriginSearchID != nil && in.OriginSearchResultID != nil {
		_ = s.search.Attribute(r.Context(), *in.OriginSearchID, *in.OriginSearchResultID, p.UserID, in.StoreID, "review_created", "review:"+id.String())
	}
	JSON(w, 201, map[string]any{"id": id})
}

func (s *Server) verifyStoreVisit(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	storeID, e := parseID(r)
	var in struct {
		Latitude       float64 `json:"latitude"`
		Longitude      float64 `json:"longitude"`
		AccuracyMeters float64 `json:"accuracy_meters"`
	}
	if e == nil {
		e = Decode(w, r, &in, 16<<10)
	}
	var verification social.VisitVerification
	if e == nil {
		verification, e = s.social.VerifyVisit(r.Context(), p.UserID, storeID, in.Latitude, in.Longitude, in.AccuracyMeters)
	}
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, http.StatusCreated, verification)
}
func (s *Server) deletePost(w http.ResponseWriter, r *http.Request) {
	s.idAction(w, r, func(p, id uuid.UUID) error { return s.social.DeletePost(r.Context(), p, id) })
}
func (s *Server) like(w http.ResponseWriter, r *http.Request) {
	s.idAction(w, r, func(p, id uuid.UUID) error { return s.social.Like(r.Context(), p, id, true) })
}
func (s *Server) unlike(w http.ResponseWriter, r *http.Request) {
	s.idAction(w, r, func(p, id uuid.UUID) error { return s.social.Like(r.Context(), p, id, false) })
}
func (s *Server) follow(w http.ResponseWriter, r *http.Request) {
	s.idAction(w, r, func(p, id uuid.UUID) error { return s.social.Follow(r.Context(), p, id, true) })
}
func (s *Server) unfollow(w http.ResponseWriter, r *http.Request) {
	s.idAction(w, r, func(p, id uuid.UUID) error { return s.social.Follow(r.Context(), p, id, false) })
}
func (s *Server) favorite(w http.ResponseWriter, r *http.Request) {
	s.favoriteAction(w, r, true)
}
func (s *Server) unfavorite(w http.ResponseWriter, r *http.Request) {
	s.favoriteAction(w, r, false)
}
func (s *Server) favoriteAction(w http.ResponseWriter, r *http.Request, add bool) {
	p, _ := appmw.PrincipalFrom(r.Context())
	storeID, e := parseID(r)
	changed := false
	if e == nil {
		changed, e = s.stores.Favorite(r.Context(), p.UserID, storeID, add)
	}
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	searchID, se := uuid.Parse(r.Header.Get("X-Origin-Search-ID"))
	resultID, re := uuid.Parse(r.Header.Get("X-Origin-Search-Result-ID"))
	if changed && se == nil && re == nil {
		event := "unfavorite"
		if add {
			event = "favorite"
		}
		_ = s.search.Attribute(r.Context(), searchID, resultID, p.UserID, storeID, event, event+":"+p.UserID.String()+":"+storeID.String())
	}
	w.WriteHeader(204)
}
func (s *Server) deleteComment(w http.ResponseWriter, r *http.Request) {
	s.idAction(w, r, func(p, id uuid.UUID) error { return s.social.DeleteComment(r.Context(), p, id) })
}
func (s *Server) addComment(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	id, e := parseID(r)
	var in struct {
		Text            string  `json:"text"`
		ContentLanguage *string `json:"content_language"`
	}
	if e == nil {
		e = Decode(w, r, &in, 16<<10)
	}
	var comment uuid.UUID
	if e == nil {
		comment, e = s.social.AddCommentLocalized(r.Context(), p.UserID, id, in.Text, in.ContentLanguage)
	}
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 201, map[string]any{"id": comment})
}
func (s *Server) idAction(w http.ResponseWriter, r *http.Request, fn func(uuid.UUID, uuid.UUID) error) {
	p, _ := appmw.PrincipalFrom(r.Context())
	id, e := parseID(r)
	if e == nil {
		e = fn(p.UserID, id)
	}
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(204)
}
