package rdb

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// stateNumber is the type of a state the database stores as a number.
type stateNumber interface {
	~int8 | ~int16
}

// applyTransition moves the row id of table to t.To with a compare-and-set,
//
//	UPDATE table SET column = $2[, extraSet] WHERE id = $1 AND column = ANY($3)
//
// so a row a concurrent writer already moved out of t.From is not
// overwritten. Zero rows affected is the lost race and fails with
// FailedPrecondition; the caller rolls its transaction back. A transition that
// changes nothing is not applied.
//
// table, column and extraSet are SQL text written in the repository, never
// input. extraSet sets further columns of the same row and numbers its
// placeholders from $4, in the order of extraArgs ("lost_reason = $4").
func applyTransition[S stateNumber](ctx context.Context, tx pgx.Tx, table, column, id string, t entity.Transition[S], extraSet string, extraArgs ...any) error {
	if !t.Changed() {
		return nil
	}
	from := make([]int16, len(t.From))
	for i, s := range t.From {
		from[i] = int16(s)
	}
	set := column + " = $2"
	if extraSet != "" {
		set += ", " + extraSet
	}
	query := "UPDATE " + table + " SET " + set + " WHERE id = $1 AND " + column + " = ANY($3)"
	tag, err := tx.Exec(ctx, query, append([]any{id, int16(t.To), from}, extraArgs...)...)
	if err != nil {
		return toAppErr(err, "failed to apply the state transition", slog.String("table", table), slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.FailedPrecondition, "the row was changed concurrently",
			slog.String("table", table), slog.String("id", id), slog.Int("to", int(t.To)))
	}
	return nil
}
