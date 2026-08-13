package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/auth"
	. "github.com/burakaltintas/home-app-api/internal/httpapi"
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

func (s *Server) Router(log *slog.Logger, bff []string, tokens *security.TokenManager, metricsToken ...string) http.Handler {
	r := chi.NewRouter()
	r.Use(appmw.RequestID, appmw.Recover(log), observability.HTTPMiddleware, appmw.Logging(log), appmw.SecurityHeaders)
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
	token := ""
	if len(metricsToken) > 0 {
		token = metricsToken[0]
	}
	r.Handle("/metrics", observability.MetricsHandler(token))
	if uploads := s.media.UploadHandler(); uploads != nil {
		r.Mount("/uploads", http.StripPrefix("/uploads", uploads))
	}
	r.Route("/v1", func(r chi.Router) {
		r.Use(appmw.BFF(bff))
		r.Use(appmw.NewLimiter(180, 40).Middleware)
		r.Use(appmw.OptionalAuth(tokens))
		searchLimit := appmw.NewLimiter(30, 8)
		writeLimit := appmw.NewLimiter(20, 6)
		socialLimit := appmw.NewLimiter(60, 15)
		r.Get("/feed", s.feed)
		r.With(searchLimit.Middleware).Post("/search", s.searchStores)
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
			r.Get("/me/searches", s.mySearches)
			r.Delete("/me/searches", s.deleteMySearches)
			r.Delete("/me/searches/{id}", s.deleteMySearch)
			r.With(writeLimit.Middleware).Post("/posts", s.createPost)
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
func (s *Server) createMediaUpload(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	var in media.CreateRequest
	if e := Decode(w, r, &in, 16<<10); e != nil {
		WriteError(w, e)
		return
	}
	x, e := s.media.Create(r.Context(), p.UserID, in)
	if e != nil {
		WriteError(w, e)
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
		WriteError(w, e)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) requestCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if e := Decode(w, r, &in, 16<<10); e != nil {
		WriteError(w, e)
		return
	}
	var visitor *uuid.UUID
	if v, ok := appmw.VisitorID(r); ok {
		visitor = &v
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if e := s.auth.RequestCode(r.Context(), in.Email, visitor, auth.IPHash(s.hashKey, ip)); e != nil {
		observability.Auth("otp_request", "failure")
		WriteError(w, e)
		return
	}
	observability.Auth("otp_request", "success")
	JSON(w, 202, map[string]string{"status": "accepted"})
}
func client(r *http.Request) auth.Client {
	return auth.Client{Type: strings.TrimSpace(r.Header.Get("X-Client-Type")), Metadata: map[string]any{"version": strings.TrimSpace(r.Header.Get("X-Client-Version"))}}
}
func (s *Server) verifyCode(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Code string }
	if e := Decode(w, r, &in, 16<<10); e != nil {
		WriteError(w, e)
		return
	}
	x, e := s.auth.VerifyCode(r.Context(), in.Email, in.Code, client(r))
	if e != nil {
		observability.Auth("otp_verify", "failure")
		WriteError(w, e)
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
		WriteError(w, e)
		return
	}
	x, e := s.auth.Google(r.Context(), in.IDToken, client(r))
	if e != nil {
		observability.Auth("google_login", "failure")
		WriteError(w, e)
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
		WriteError(w, e)
		return
	}
	x, e := s.auth.Refresh(r.Context(), in.RefreshToken, client(r))
	if e != nil {
		observability.Auth("refresh", "failure")
		WriteError(w, e)
		return
	}
	observability.Auth("refresh", "success")
	JSON(w, 200, x)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	if e := s.auth.Logout(r.Context(), p.UserID, p.SessionID, false); e != nil {
		WriteError(w, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	if e := s.auth.Logout(r.Context(), p.UserID, p.SessionID, true); e != nil {
		WriteError(w, e)
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
	items, next, e := s.social.Feed(r.Context(), viewer(r), r.URL.Query().Get("cursor"), queryInt(r, "limit", 20))
	if e != nil {
		WriteError(w, e)
		return
	}
	JSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}
func (s *Server) storeSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	lat, e := queryFloat(r, "latitude")
	lon, lonErr := queryFloat(r, "longitude")
	radius := queryInt(r, "radius_meters", 10000)
	if e != nil || lonErr != nil || (lat == nil) != (lon == nil) || (lat != nil && !storepkg.ValidCoordinates(*lat, *lon)) || radius < 100 || radius > 50000 || len(r.URL.Query().Get("q")) > 500 {
		WriteError(w, ErrInvalidInput)
		return
	}
	items, e := s.stores.Search(r.Context(), r.URL.Query().Get("q"), nil, "", lat, lon, radius, queryInt(r, "limit", 20), viewer(r))
	if e != nil {
		WriteError(w, e)
		return
	}
	var visitor *uuid.UUID
	if id, ok := appmw.VisitorID(r); ok {
		visitor = &id
	}
	searchID, visitor, e := s.search.RecordInternalSearch(r.Context(), viewer(r), visitor, searchpkg.Request{Query: r.URL.Query().Get("q"), Latitude: lat, Longitude: lon, RadiusMeters: radius}, items, time.Since(start))
	if e != nil {
		WriteError(w, e)
		return
	}
	JSON(w, 200, map[string]any{"search_id": searchID, "visitor_session_id": visitor, "items": items})
}
func (s *Server) storeDetail(w http.ResponseWriter, r *http.Request) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e)
		return
	}
	lat, latErr := queryFloat(r, "latitude")
	lon, lonErr := queryFloat(r, "longitude")
	if latErr != nil || lonErr != nil || (lat == nil) != (lon == nil) || (lat != nil && !storepkg.ValidCoordinates(*lat, *lon)) {
		WriteError(w, ErrInvalidInput)
		return
	}
	x, e := s.stores.Get(r.Context(), id, viewer(r), lat, lon)
	if e != nil {
		WriteError(w, e)
		return
	}
	posts, e := s.social.PostsBy(r.Context(), "store_id", id, viewer(r), 5)
	if e != nil {
		WriteError(w, e)
		return
	}
	JSON(w, 200, map[string]any{"store": x, "recent_posts": posts})
}
func (s *Server) postDetail(w http.ResponseWriter, r *http.Request) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e)
		return
	}
	x, e := s.social.GetPost(r.Context(), id, viewer(r))
	if e != nil {
		WriteError(w, e)
		return
	}
	JSON(w, 200, x)
}
func (s *Server) comments(w http.ResponseWriter, r *http.Request) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e)
		return
	}
	x, e := s.social.Comments(r.Context(), id, queryInt(r, "limit", 50))
	if e != nil {
		WriteError(w, e)
		return
	}
	JSON(w, 200, map[string]any{"items": x})
}
func (s *Server) postsByStore(w http.ResponseWriter, r *http.Request) { s.postsBy(w, r, "store_id") }
func (s *Server) postsByUser(w http.ResponseWriter, r *http.Request)  { s.postsBy(w, r, "user_id") }
func (s *Server) postsBy(w http.ResponseWriter, r *http.Request, column string) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e)
		return
	}
	x, e := s.social.PostsBy(r.Context(), column, id, viewer(r), queryInt(r, "limit", 20))
	if e != nil {
		WriteError(w, e)
		return
	}
	JSON(w, 200, map[string]any{"items": x})
}
func (s *Server) userPublic(w http.ResponseWriter, r *http.Request) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e)
		return
	}
	x, e := s.users.Public(r.Context(), id)
	if e != nil {
		WriteError(w, e)
		return
	}
	JSON(w, 200, x)
}

func (s *Server) searchStores(w http.ResponseWriter, r *http.Request) {
	var in searchpkg.Request
	if e := Decode(w, r, &in, 64<<10); e != nil {
		WriteError(w, e)
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
		WriteError(w, e)
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
		WriteError(w, e)
		return
	}
	if in.Provider != "google" {
		WriteError(w, ErrInvalidInput)
		return
	}
	id, e := s.search.MaterializeGoogleStore(r.Context(), in.PlaceID)
	if e != nil {
		WriteError(w, e)
		return
	}
	JSON(w, 200, map[string]any{"id": id})
}
func (s *Server) interaction(w http.ResponseWriter, r *http.Request) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e)
		return
	}
	var in struct {
		SearchResultID *uuid.UUID `json:"search_result_id"`
		EventType      string     `json:"event_type"`
		IdempotencyKey string     `json:"idempotency_key"`
	}
	if e = Decode(w, r, &in, 16<<10); e != nil {
		WriteError(w, e)
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
		WriteError(w, e)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	x, e := s.users.Me(r.Context(), p.UserID)
	if e != nil {
		WriteError(w, e)
		return
	}
	JSON(w, 200, x)
}
func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	var in userpkg.Update
	if e := Decode(w, r, &in, 64<<10); e != nil {
		WriteError(w, e)
		return
	}
	if e := s.users.Update(r.Context(), p.UserID, in); e != nil {
		WriteError(w, e)
		return
	}
	s.me(w, r)
}
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	if e := s.users.DeleteAccount(r.Context(), p.UserID); e != nil {
		WriteError(w, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) mySearches(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	x, e := s.users.Searches(r.Context(), p.UserID, queryInt(r, "limit", 30))
	if e != nil {
		WriteError(w, e)
		return
	}
	JSON(w, 200, map[string]any{"items": x})
}
func (s *Server) deleteMySearches(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	if e := s.users.DeleteSearches(r.Context(), p.UserID, nil); e != nil {
		WriteError(w, e)
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
		WriteError(w, e)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) createPost(w http.ResponseWriter, r *http.Request) {
	p, _ := appmw.PrincipalFrom(r.Context())
	var in social.CreatePost
	if e := Decode(w, r, &in, 128<<10); e != nil {
		WriteError(w, e)
		return
	}
	id, e := s.social.CreatePost(r.Context(), p.UserID, in)
	if e != nil {
		WriteError(w, e)
		return
	}
	if in.OriginSearchID != nil && in.OriginSearchResultID != nil {
		_ = s.search.Attribute(r.Context(), *in.OriginSearchID, *in.OriginSearchResultID, p.UserID, in.StoreID, "review_created", "review:"+id.String())
	}
	JSON(w, 201, map[string]any{"id": id})
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
		WriteError(w, e)
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
		Text string `json:"text"`
	}
	if e == nil {
		e = Decode(w, r, &in, 16<<10)
	}
	var comment uuid.UUID
	if e == nil {
		comment, e = s.social.AddComment(r.Context(), p.UserID, id, in.Text)
	}
	if e != nil {
		WriteError(w, e)
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
		WriteError(w, e)
		return
	}
	w.WriteHeader(204)
}
