package store

import (
	"time"

	"alpheus/kernel/internal/units"
)

type AgentPaperAccount struct {
	AccountID    string
	AccountType  string
	StartingCash units.Micros
	Cash         units.Micros
	BuyingPower  units.Micros
	Generation   int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AgentPaperPosition struct {
	AccountID  string
	Symbol     string
	Kind       string
	Multiplier int64
	Qty        units.Qty
	AvgPrice   units.Micros
	Generation int64
	OpenedAt   time.Time
	UpdatedAt  time.Time
}

func (s *Store) AgentPaperPortfolio(
	accountID string,
) (AgentPaperAccount, []AgentPaperPosition, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	var account AgentPaperAccount
	var startingCash, cash, buyingPower int64
	err := s.DB.QueryRowContext(ctx, `SELECT
		account_id,account_type,starting_cash_micros,cash_micros,
		buying_power_micros,generation,created_at,updated_at
		FROM agent_paper_account WHERE account_id=$1`,
		accountID,
	).Scan(
		&account.AccountID, &account.AccountType, &startingCash, &cash,
		&buyingPower, &account.Generation, &account.CreatedAt,
		&account.UpdatedAt,
	)
	if err != nil {
		return AgentPaperAccount{}, nil, normalizeDBError(err)
	}
	account.StartingCash = units.Micros(startingCash)
	account.Cash = units.Micros(cash)
	account.BuyingPower = units.Micros(buyingPower)
	rows, err := s.DB.QueryContext(ctx, `SELECT
		account_id,symbol,kind,multiplier,qty,avg_price_micros,
		generation,opened_at,updated_at
		FROM agent_paper_position WHERE account_id=$1 ORDER BY symbol`,
		accountID,
	)
	if err != nil {
		return AgentPaperAccount{}, nil, normalizeDBError(err)
	}
	defer rows.Close()
	positions := make([]AgentPaperPosition, 0)
	for rows.Next() {
		var position AgentPaperPosition
		var qty, avgPrice int64
		if err := rows.Scan(
			&position.AccountID, &position.Symbol, &position.Kind,
			&position.Multiplier, &qty, &avgPrice, &position.Generation,
			&position.OpenedAt, &position.UpdatedAt,
		); err != nil {
			return AgentPaperAccount{}, nil, normalizeDBError(err)
		}
		position.Qty = units.Qty(qty)
		position.AvgPrice = units.Micros(avgPrice)
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		return AgentPaperAccount{}, nil, normalizeDBError(err)
	}
	return account, positions, nil
}
