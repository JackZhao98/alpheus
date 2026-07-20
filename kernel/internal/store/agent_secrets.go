package store

import "database/sql"

type AgentSecretRecord struct {
	Name       string
	Ciphertext []byte
}

func (s *Store) PutAgentSecret(name string, ciphertext []byte) error {
	ctx, cancel := s.deadline()
	defer cancel()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO agent_secret (name,ciphertext)
		VALUES ($1,$2) ON CONFLICT (name) DO UPDATE
		SET ciphertext=EXCLUDED.ciphertext,updated_at=clock_timestamp()`, name, ciphertext)
	return normalizeDBError(err)
}

func (s *Store) GetAgentSecret(name string) (*AgentSecretRecord, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	record := &AgentSecretRecord{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT name,ciphertext FROM agent_secret WHERE name=$1`, name).
		Scan(&record.Name, &record.Ciphertext)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	return record, nil
}

func (s *Store) DeleteAgentSecret(name string) error {
	ctx, cancel := s.deadline()
	defer cancel()
	_, err := s.DB.ExecContext(ctx, `DELETE FROM agent_secret WHERE name=$1`, name)
	return normalizeDBError(err)
}

func (s *Store) ListAgentSecretNames() ([]string, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `SELECT name FROM agent_secret ORDER BY name`)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	names := make([]string, 0, 2)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, normalizeDBError(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, normalizeDBError(err)
	}
	return names, nil
}
