package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type providerSnapshot struct {
	Observation *store.BrokerObservation
	View        *store.BrokerAccountView
	Account     broker.AccountState
	Positions   []broker.Position
	Orders      []broker.ReadOrder
}

type observedAccount struct {
	AccountID     string       `json:"account_id"`
	AccountType   string       `json:"account_type"`
	BuyingPower   units.Micros `json:"buying_power"`
	Equity        units.Micros `json:"equity"`
	EquityKnown   bool         `json:"equity_known"`
	DayTradesUsed int          `json:"day_trades_used"`
	Cash          units.Micros `json:"cash"`
	CashKnown     bool         `json:"cash_known"`
	Source        string       `json:"source"`
	AsOf          time.Time    `json:"as_of"`
}

func (s *server) captureProviderSnapshot(ctx context.Context, purpose string) (*providerSnapshot, error) {
	provider := s.accountProvider()
	if provider == nil {
		return nil, fmt.Errorf("account provider unavailable")
	}
	started := time.Now().UTC()
	accountIDCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	accountID, err := provider.AccountID(accountIDCtx)
	cancel()
	accountID = strings.TrimSpace(accountID)
	if err != nil || accountID == "" {
		return nil, fmt.Errorf("provider account identity unavailable")
	}
	if expected := strings.TrimSpace(s.mode.LiveAccountID); expected != "" && accountID != expected {
		return nil, fmt.Errorf("provider account binding mismatch")
	}

	snapshot := &providerSnapshot{}
	families := make([]store.BrokerObservationFamilyInput, 0, 3)
	source := "provider"

	accountCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	account, accountErr := provider.Account(accountCtx)
	cancel()
	accountCompleted := time.Now().UTC()
	accountFamily := store.BrokerObservationFamilyInput{
		Family: store.BrokerFamilyAccount, Status: "error", ErrorCode: "unavailable", CompletedAt: accountCompleted,
	}
	if accountErr == nil && account.ExternalID == accountID && account.Source != "" && !account.AsOf.IsZero() {
		snapshot.Account = account
		source = account.Source
		accountFamily.Status, accountFamily.ErrorCode = "success", ""
		accountFamily.Items = []store.BrokerObservationItemInput{{
			ObjectKey: accountID, ObservedAt: account.AsOf,
			Canonical: observedAccount{
				AccountID: accountID, AccountType: account.AccountType, BuyingPower: account.BuyingPower,
				Equity: account.Equity, EquityKnown: account.EquityKnown, DayTradesUsed: account.DayTradesUsed,
				Cash:      account.Cash,
				CashKnown: account.CashKnown, Source: account.Source, AsOf: account.AsOf,
			},
		}}
	} else if accountErr == nil {
		accountFamily.ErrorCode = "wrong_account"
	}
	families = append(families, accountFamily)

	positionsCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	positions, positionsErr := provider.Positions(positionsCtx)
	cancel()
	positionsCompleted := time.Now().UTC()
	positionsFamily := store.BrokerObservationFamilyInput{
		Family: store.BrokerFamilyPositions, Status: "error", ErrorCode: "unavailable", CompletedAt: positionsCompleted,
	}
	if positionsErr == nil && brokerPositionsValid(positions, accountID, source, positionsCompleted) {
		snapshot.Positions = positions
		positionsFamily.Status, positionsFamily.ErrorCode = "success", ""
		for _, position := range positions {
			positionsFamily.Items = append(positionsFamily.Items, store.BrokerObservationItemInput{
				ObjectKey: position.PositionID, ObservedAt: position.AsOf, Canonical: position,
			})
		}
	} else if positionsErr == nil {
		positionsFamily.ErrorCode = "invalid"
	}
	families = append(families, positionsFamily)

	ordersCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	orders, ordersErr := provider.OpenOrders(ordersCtx)
	cancel()
	ordersCompleted := time.Now().UTC()
	ordersFamily := store.BrokerObservationFamilyInput{
		Family: store.BrokerFamilyOrders, Status: "error", ErrorCode: "unavailable", CompletedAt: ordersCompleted,
	}
	if ordersErr == nil && brokerOrdersValid(orders, source, ordersCompleted) {
		snapshot.Orders = orders
		ordersFamily.Status, ordersFamily.ErrorCode = "success", ""
		for _, order := range orders {
			ordersFamily.Items = append(ordersFamily.Items, store.BrokerObservationItemInput{
				ObjectKey: order.BrokerOrderID, ObservedAt: order.AsOf, Canonical: order,
			})
		}
	} else if ordersErr == nil {
		ordersFamily.ErrorCode = "invalid"
	}
	families = append(families, ordersFamily)

	completed := time.Now().UTC()
	observation, err := s.store.RecordBrokerObservation(store.BrokerObservationInput{
		AccountID: accountID, Source: source, Purpose: purpose, StartedAt: started,
		CompletedAt: completed, Families: families,
	})
	if err != nil {
		return nil, err
	}
	snapshot.Observation = observation
	if observation.Status != "complete" {
		return snapshot, fmt.Errorf("provider snapshot is partial")
	}
	view, err := s.store.LoadBrokerAccountView(accountID)
	if err != nil {
		return nil, err
	}
	snapshot.View = view
	return snapshot, nil
}

func (s *server) captureProviderFills(ctx context.Context, purpose, accountID, source string, since time.Time) ([]broker.ReadFill, *store.BrokerObservation, []store.BrokerObservedObject, error) {
	started := time.Now().UTC()
	providerCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	fills, providerErr := s.accountProvider().RecentFills(providerCtx, since)
	cancel()
	completed := time.Now().UTC()
	family := store.BrokerObservationFamilyInput{
		Family: store.BrokerFamilyFills, Status: "error", ErrorCode: "unavailable", CompletedAt: completed,
	}
	if providerErr == nil && brokerFillsValid(fills, source, completed) {
		family.Status, family.ErrorCode = "success", ""
		for _, fill := range fills {
			family.Items = append(family.Items, store.BrokerObservationItemInput{
				ObjectKey: fill.FillID, ObservedAt: fill.AsOf, Canonical: fill,
			})
		}
	} else if providerErr == nil {
		family.ErrorCode = "invalid"
	}
	observation, err := s.store.RecordBrokerObservation(store.BrokerObservationInput{
		AccountID: accountID, Source: source, Purpose: purpose, StartedAt: started,
		CompletedAt: completed, Families: []store.BrokerObservationFamilyInput{family},
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if observation.Status != "complete" {
		return nil, observation, nil, fmt.Errorf("provider fill snapshot is partial")
	}
	view, err := s.store.LoadBrokerObservation(observation.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	return fills, observation, view.Objects, nil
}

func brokerPositionsValid(positions []broker.Position, accountID, source string, completedAt time.Time) bool {
	seen := map[string]bool{}
	for _, position := range positions {
		if position.PositionID == "" || position.Symbol == "" || position.Qty == 0 ||
			(position.Kind != "equity" && position.Kind != "option") || position.Multiplier <= 0 ||
			position.Source != source || position.AsOf.IsZero() || position.AsOf.After(completedAt) || seen[position.PositionID] {
			return false
		}
		seen[position.PositionID] = true
	}
	return accountID != ""
}

func brokerOrdersValid(orders []broker.ReadOrder, source string, completedAt time.Time) bool {
	seen := map[string]bool{}
	for _, order := range orders {
		if order.BrokerOrderID == "" || order.Symbol == "" || (order.Side != "buy" && order.Side != "sell") ||
			(order.Kind != "equity" && order.Kind != "option") ||
			(order.PositionEffect != "open" && order.PositionEffect != "close" && order.PositionEffect != "unknown") ||
			order.Qty <= 0 || order.FilledQty < 0 || order.FilledQty > order.Qty ||
			order.Source != source || order.AsOf.IsZero() || order.AsOf.After(completedAt) || seen[order.BrokerOrderID] {
			return false
		}
		if order.LimitPriceKnown && order.LimitPrice <= 0 {
			return false
		}
		seen[order.BrokerOrderID] = true
	}
	return true
}

func brokerFillsValid(fills []broker.ReadFill, source string, completedAt time.Time) bool {
	seen := map[string]bool{}
	for _, fill := range fills {
		if fill.FillID == "" || fill.BrokerOrderID == "" || fill.Symbol == "" ||
			(fill.Side != "buy" && fill.Side != "sell") || fill.Qty <= 0 || fill.Price <= 0 || fill.Fees < 0 ||
			fill.Source != source || fill.AsOf.IsZero() || fill.AsOf.After(completedAt) || seen[fill.FillID] {
			return false
		}
		seen[fill.FillID] = true
	}
	return true
}
