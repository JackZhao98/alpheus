package store

import "time"

// ControlWarning is an operator-visible execution uncertainty. The Cockpit
// never receives release or retry capability; M11 may attach a separately
// verified exact candidate for two-step human adoption.
type ControlWarning struct {
	Kind                   string    `json:"kind"`
	ID                     string    `json:"id"`
	OperationID            string    `json:"operation_id"`
	Ledger                 string    `json:"ledger,omitempty"`
	Symbol                 string    `json:"symbol,omitempty"`
	State                  string    `json:"state"`
	CreatedAt              time.Time `json:"created_at"`
	Detail                 string    `json:"detail,omitempty"`
	ProviderErrorCode      string    `json:"provider_error_code,omitempty"`
	CandidateBrokerOrderID string    `json:"candidate_broker_order_id,omitempty"`
}

func (s *Store) ListControlWarnings(pendingBefore, claimBefore time.Time, limit int) ([]ControlWarning, error) {
	if pendingBefore.IsZero() || claimBefore.IsZero() || limit < 1 || limit > 500 {
		return nil, ErrInvalidControlWarningQuery
	}
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `
		SELECT kind,id,operation_id,ledger,symbol,state,created_at,detail,provider_error_code,candidate_broker_order_id
		FROM (
			SELECT 'execution_attempt'::text AS kind, a.id, a.operation_id,
				COALESCE(ro.ledger,rc.ledger,'') AS ledger,
				COALESCE(ro.symbol,rc.symbol,op.payload->>'symbol',op.payload->>'underlying','') AS symbol,
				a.state, a.created_at, COALESCE(a.last_error,'') AS detail,
				COALESCE(a.provider_error_code,''),COALESCE(a.candidate_broker_order_id::text,'')
			FROM execution_attempt a
			JOIN operations op ON op.id=a.operation_id
			LEFT JOIN open_reservation ro ON ro.id=a.open_reservation_id
			LEFT JOIN close_reservation rc ON rc.id=a.close_reservation_id
			WHERE a.state='unknown'
			   OR (a.state='pending' AND a.created_at < $1)
			   OR (a.state='claimed' AND a.claimed_at < $2)
			UNION ALL
			SELECT 'open_reservation',r.id,r.operation_id,r.ledger,r.symbol,
				r.resource_state,r.created_at,'funds and risk remain reserved','',''
			FROM open_reservation r WHERE r.resource_state='held'
			UNION ALL
			SELECT 'close_reservation',r.id,r.operation_id,r.ledger,r.symbol,
				r.state,r.created_at,'position quantity remains reserved','',''
			FROM close_reservation r WHERE r.state='held'
		) warning
		ORDER BY created_at,id LIMIT $3`, pendingBefore, claimBefore, limit)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	warnings := make([]ControlWarning, 0, limit)
	for rows.Next() {
		var warning ControlWarning
		if err := rows.Scan(&warning.Kind, &warning.ID, &warning.OperationID,
			&warning.Ledger, &warning.Symbol, &warning.State, &warning.CreatedAt,
			&warning.Detail, &warning.ProviderErrorCode, &warning.CandidateBrokerOrderID); err != nil {
			return nil, normalizeDBError(err)
		}
		warnings = append(warnings, warning)
	}
	return warnings, normalizeDBError(rows.Err())
}
