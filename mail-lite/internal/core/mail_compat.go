package core

// Compatibility helpers used by the sales module port.  The original
// go-version service had its own HTTP error and auth helpers; mail-lite keeps
// authentication in the parent admin middleware and exposes the same behavior
// through these small adapters.

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

type mailFailure struct {
	code    int
	message string
}

func (e mailFailure) Error() string { return e.message }
func badMail(message string) error  { return mailFailure{code: http.StatusBadRequest, message: message} }

func mailError(w http.ResponseWriter, err error) {
	var failure mailFailure
	switch {
	case errors.As(err, &failure):
		respond(w, failure.code, map[string]string{"error": failure.message})
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, ErrNotFound):
		respond(w, http.StatusNotFound, map[string]string{"error": "邮箱或记录不存在"})
	case errors.Is(err, ErrConflict), strings.Contains(err.Error(), "UNIQUE constraint"):
		respond(w, http.StatusConflict, map[string]string{"error": "记录已变更或已存在"})
	default:
		fail(w, err)
	}
}

func uniqueMailIDs(ids []int64) ([]int64, error) { return uniqueMailInventoryIDs(ids) }
func marks(ids []int64) (string, []any)          { return mailMarks(ids) }
func requireMailIDs(tx *sql.Tx, ids []int64, kind string) error {
	return requireMailInventoryIDs(tx, ids, kind)
}
