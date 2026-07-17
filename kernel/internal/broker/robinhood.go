// FILL POINT #3 — real venue adapter.
//
// Wire this through the Robinhood MCP (official Go SDK:
// github.com/modelcontextprotocol/go-sdk). Keep ONE thin call site
// (mcp.CallTool) with caching + rate limiting + retry, then normalize
// tool outputs into the types in broker.go. Read-only methods
// (GetAccount/GetPositions/GetQuote) can land in Phase 1-2; order
// placement stays gated behind M11. See docs/plan/INDEX.md.
// Credentials live HERE and only here.
package broker

import (
	"errors"

	"alpheus/kernel/internal/units"
)

var errTODO = errors.New("robinhood adapter not implemented yet — see plan milestones M8A (reads) / M11 (orders)")

type Robinhood struct{}

func (r *Robinhood) GetAccount() (AccountState, error)     { return AccountState{}, errTODO }
func (r *Robinhood) GetPositions() ([]Position, error)     { return nil, errTODO }
func (r *Robinhood) GetQuote(symbol string) (Quote, error) { return Quote{}, errTODO }
func (r *Robinhood) GetInstrument(symbol string) (Instrument, error) {
	return Instrument{}, errTODO
}
func (r *Robinhood) PlaceLimitOrder(symbol, side string, qty units.Qty, limit units.Micros, kind string) (OrderResult, error) {
	return OrderResult{}, errTODO
}
func (r *Robinhood) CancelOrder(id string) (OrderResult, error) { return OrderResult{}, errTODO }
func (r *Robinhood) GetOrder(id string) (OrderResult, error)    { return OrderResult{}, errTODO }
