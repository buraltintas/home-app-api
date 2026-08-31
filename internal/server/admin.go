package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	. "github.com/burakaltintas/home-app-api/internal/httpapi"
	appmw "github.com/burakaltintas/home-app-api/internal/middleware"
	"github.com/google/uuid"
)

// The admin handlers are thin: the reporting service already computes every metric below,
// it simply had no route. Raw-data reads go through internal/admin.

func adminRange(r *http.Request) (time.Time, time.Time) {
	to := time.Now()
	from := to.AddDate(0, 0, -30)
	if v := r.URL.Query().Get("from"); v != "" {
		if parsed, e := time.Parse("2006-01-02", v); e == nil {
			from = parsed
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if parsed, e := time.Parse("2006-01-02", v); e == nil {
			to = parsed
		}
	}
	return from, to
}

func adminPage(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (s *Server) adminActor(r *http.Request) (uuid.UUID, string, bool) {
	principal, ok := appmw.PrincipalFrom(r.Context())
	if !ok {
		return uuid.Nil, "", false
	}
	return principal.UserID, appmw.AdminEmailFrom(r.Context()), true
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	from, to := adminRange(r)
	snapshot, e := s.report.GetPlatformSnapshot(r.Context())
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	daily, e := s.report.GetDailyMetrics(r.Context(), from, to)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"snapshot": snapshot, "daily": daily})
}

func (s *Server) adminSearchInsights(w http.ResponseWriter, r *http.Request) {
	from, to := adminRange(r)
	ctx := r.Context()
	overview, e := s.report.GetSearchOverview(ctx, from, to)
	if e != nil {
		WriteError(w, e, ctx)
		return
	}
	top, e := s.report.GetTopSearchQueries(ctx, from, to, 20, false)
	if e != nil {
		WriteError(w, e, ctx)
		return
	}
	// Queries that returned nothing are the most actionable thing on this page: each one
	// is somebody who asked for something the catalogue could not answer.
	zero, e := s.report.GetZeroResultQueries(ctx, from, to, 20)
	if e != nil {
		WriteError(w, e, ctx)
		return
	}
	funnel, e := s.report.GetSearchConversionFunnel(ctx, from, to)
	if e != nil {
		WriteError(w, e, ctx)
		return
	}
	categories, e := s.report.GetTopSearchCategories(ctx, from, to, 15)
	if e != nil {
		WriteError(w, e, ctx)
		return
	}
	locations, e := s.report.GetTopSearchLocations(ctx, from, to, 15)
	if e != nil {
		WriteError(w, e, ctx)
		return
	}
	JSON(w, 200, map[string]any{"overview": overview, "top_queries": top, "zero_result_queries": zero, "funnel": funnel, "categories": categories, "locations": locations})
}

func (s *Server) adminStoreInsights(w http.ResponseWriter, r *http.Request) {
	from, to := adminRange(r)
	ctx := r.Context()
	impressed, e := s.report.GetMostImpressedStores(ctx, from, to, 20)
	if e != nil {
		WriteError(w, e, ctx)
		return
	}
	clicked, e := s.report.GetMostClickedStores(ctx, from, to, 20)
	if e != nil {
		WriteError(w, e, ctx)
		return
	}
	// Wanted often, reviewed rarely: the stores worth recruiting reviews for.
	demand, e := s.report.GetHighDemandLowReviewStores(ctx, from, to, 20)
	if e != nil {
		WriteError(w, e, ctx)
		return
	}
	JSON(w, 200, map[string]any{"most_impressed": impressed, "most_clicked": clicked, "high_demand_low_review": demand})
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset := adminPage(r)
	items, e := s.admin.Users(r.Context(), r.URL.Query().Get("q"), limit, offset)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) adminStores(w http.ResponseWriter, r *http.Request) {
	limit, offset := adminPage(r)
	items, e := s.admin.Stores(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("premium") == "true", limit, offset)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) adminReviews(w http.ResponseWriter, r *http.Request) {
	limit, offset := adminPage(r)
	items, e := s.admin.Reviews(r.Context(), r.URL.Query().Get("q"), limit, offset)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) adminSearches(w http.ResponseWriter, r *http.Request) {
	limit, offset := adminPage(r)
	items, e := s.admin.Searches(r.Context(), r.URL.Query().Get("q"), limit, offset)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) adminSearchResults(w http.ResponseWriter, r *http.Request) {
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	items, e := s.admin.SearchResults(r.Context(), id)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	limit, offset := adminPage(r)
	items, e := s.admin.Audit(r.Context(), limit, offset)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) adminSetPremium(w http.ResponseWriter, r *http.Request) {
	actor, email, ok := s.adminActor(r)
	if !ok {
		WriteError(w, ErrAuthRequired, r.Context())
		return
	}
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var body struct {
		IsPremium bool `json:"is_premium"`
	}
	if e = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); e != nil {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	if e = s.admin.SetStorePremium(r.Context(), actor, email, id, body.IsPremium); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"id": id, "is_premium": body.IsPremium})
}

func (s *Server) adminSetCatalogStore(w http.ResponseWriter, r *http.Request) {
	actor, email, ok := s.adminActor(r)
	if !ok {
		WriteError(w, ErrAuthRequired, r.Context())
		return
	}
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var body struct {
		IsCatalogStore bool `json:"is_catalog_store"`
	}
	if e = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); e != nil {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	if e = s.admin.SetStoreCatalogStatus(r.Context(), actor, email, id, body.IsCatalogStore); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"id": id, "is_catalog_store": body.IsCatalogStore})
}

func (s *Server) adminSetStoreCover(w http.ResponseWriter, r *http.Request) {
	actor, email, ok := s.adminActor(r)
	if !ok {
		WriteError(w, ErrAuthRequired, r.Context())
		return
	}
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var body struct {
		MediaID uuid.UUID `json:"media_id"`
	}
	if e = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); e != nil || body.MediaID == uuid.Nil {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	if e = s.admin.SetStoreCover(r.Context(), actor, email, id, &body.MediaID); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, http.StatusOK, map[string]any{"id": id, "cover_media_id": body.MediaID})
}

func (s *Server) adminClearStoreCover(w http.ResponseWriter, r *http.Request) {
	actor, email, ok := s.adminActor(r)
	if !ok {
		WriteError(w, ErrAuthRequired, r.Context())
		return
	}
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	if e = s.admin.SetStoreCover(r.Context(), actor, email, id, nil); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminSetUserStatus(w http.ResponseWriter, r *http.Request) {
	actor, email, ok := s.adminActor(r)
	if !ok {
		WriteError(w, ErrAuthRequired, r.Context())
		return
	}
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if e = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); e != nil {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	if e = s.admin.SetUserStatus(r.Context(), actor, email, id, body.Status); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"id": id, "status": body.Status})
}

// adminDeleteUser runs the ordinary account deletion so that an administrator deleting an
// account and a person deleting their own cannot drift apart, and the published
// account-deletion page keeps describing both accurately.
func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor, email, ok := s.adminActor(r)
	if !ok {
		WriteError(w, ErrAuthRequired, r.Context())
		return
	}
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	if id == actor {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	if e = s.users.DeleteAccount(r.Context(), id); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	if e = s.admin.RecordUserDeletion(r.Context(), actor, email, id); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminDeleteReview(w http.ResponseWriter, r *http.Request) {
	actor, email, ok := s.adminActor(r)
	if !ok {
		WriteError(w, ErrAuthRequired, r.Context())
		return
	}
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	if e = s.admin.DeleteReview(r.Context(), actor, email, id); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminCategories(w http.ResponseWriter, r *http.Request) {
	items, e := s.admin.Categories(r.Context())
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": items})
}

// The editor replaces a store's categories outright rather than adding to them, so the
// screen shows the whole truth and saving it cannot leave a stale one behind.
func (s *Server) adminSetStoreCategories(w http.ResponseWriter, r *http.Request) {
	actor, email, ok := s.adminActor(r)
	if !ok {
		WriteError(w, ErrAuthRequired, r.Context())
		return
	}
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var body struct {
		Slugs []string `json:"slugs"`
	}
	if e = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); e != nil {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	if len(body.Slugs) > 13 {
		WriteError(w, ErrInvalidInput, r.Context())
		return
	}
	if e = s.admin.SetStoreCategories(r.Context(), actor, email, id, body.Slugs); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"id": id, "slugs": body.Slugs})
}

func (s *Server) adminFeedback(w http.ResponseWriter, r *http.Request) {
	limit, offset := adminPage(r)
	items, e := s.admin.Feedback(r.Context(), r.URL.Query().Get("q"), limit, offset)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) adminSetFeedbackStatus(w http.ResponseWriter, r *http.Request) {
	actor, email, ok := s.adminActor(r)
	if !ok {
		WriteError(w, ErrAuthRequired, r.Context())
		return
	}
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if e := Decode(w, r, &in, 1<<10); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	if e := s.admin.SetFeedbackStatus(r.Context(), actor, email, id, in.Status); e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) adminReplyFeedback(w http.ResponseWriter, r *http.Request) {
	actor, email, ok := s.adminActor(r)
	if !ok {
		WriteError(w, ErrAuthRequired, r.Context())
		return
	}
	id, e := parseID(r)
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	var in struct {
		Message string `json:"message"`
	}
	if e = Decode(w, r, &in, 16<<10); e == nil {
		e = s.admin.ReplyFeedback(r.Context(), actor, email, id, in.Message)
	}
	if e != nil {
		WriteError(w, e, r.Context())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
