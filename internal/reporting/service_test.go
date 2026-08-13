package reporting

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	pgxmock "github.com/pashagolub/pgxmock/v4"
)

func TestRecordTxUpdatesSnapshotOnce(t *testing.T) {
	ctx := context.Background()
	mock, e := pgxmock.NewPool()
	if e != nil {
		t.Fatal(e)
	}
	defer mock.Close()
	mock.ExpectBegin()
	tx, e := mock.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO platform_events")).WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("event-id"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE platform_stats")).WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	changed, e := (&Service{now: func() time.Time { return time.Unix(1, 0) }}).RecordTx(ctx, tx, Event{Type: FavoriteCreated, IdempotencyKey: "favorite-1"})
	if e != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, e)
	}
	mock.ExpectRollback()
	_ = tx.Rollback(ctx)
	if e = mock.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestRecordTxDuplicateDoesNotTouchSnapshot(t *testing.T) {
	ctx := context.Background()
	mock, e := pgxmock.NewPool()
	if e != nil {
		t.Fatal(e)
	}
	defer mock.Close()
	mock.ExpectBegin()
	tx, _ := mock.Begin(ctx)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO platform_events")).WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnError(pgx.ErrNoRows)
	changed, e := (&Service{now: time.Now}).RecordTx(ctx, tx, Event{Type: FavoriteCreated, IdempotencyKey: "same-event"})
	if e != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, e)
	}
	mock.ExpectRollback()
	_ = tx.Rollback(ctx)
	if e = mock.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
