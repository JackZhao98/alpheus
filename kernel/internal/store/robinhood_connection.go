package store

import (
	"database/sql"
	"time"
)

// RobinhoodOAuthFlow holds the server-only half of a short-lived PKCE flow.
// State is stored only as a digest; verifier ciphertext is already encrypted
// by Kernel before it enters PostgreSQL.
type RobinhoodOAuthFlow struct {
	ID                 string
	StateDigest        string
	VerifierCiphertext []byte
	ClientID           string
	RedirectURI        string
	Subject            string
	ExpiresAt          time.Time
}

func (s *Store) CreateRobinhoodOAuthFlow(flow RobinhoodOAuthFlow) error {
	ctx, cancel := s.deadline()
	defer cancel()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO robinhood_oauth_flow
		(id,state_digest,verifier_ciphertext,client_id,redirect_uri,subject,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		flow.ID, flow.StateDigest, flow.VerifierCiphertext, flow.ClientID, flow.RedirectURI, flow.Subject, flow.ExpiresAt)
	return normalizeDBError(err)
}

// ConsumeRobinhoodOAuthFlow atomically claims one valid callback state. A
// replay, expired state, or unknown state returns nil without leaking which.
func (s *Store) ConsumeRobinhoodOAuthFlow(stateDigest string) (*RobinhoodOAuthFlow, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	flow := &RobinhoodOAuthFlow{StateDigest: stateDigest}
	err := s.DB.QueryRowContext(ctx, `UPDATE robinhood_oauth_flow
		SET status='consumed', consumed_at=clock_timestamp()
		WHERE state_digest=$1 AND status='pending' AND expires_at > clock_timestamp()
		RETURNING id,verifier_ciphertext,client_id,redirect_uri,subject,expires_at`, stateDigest).
		Scan(&flow.ID, &flow.VerifierCiphertext, &flow.ClientID, &flow.RedirectURI, &flow.Subject, &flow.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	return flow, nil
}
