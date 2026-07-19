package store

import (
	"fmt"

	"alpheus/kernel/internal/units"
)

// ExternalControlEpisode is Kernel-derived evidence for a new management
// lifecycle over an external broker order or an external/mixed position. It
// records an accounting boundary; it never adopts the earlier broker action.
type ExternalControlEpisode struct {
	ID                    string
	OperationID           string
	ControlAction         string
	Origin                string
	BrokerObservationID   string
	ObservationGeneration int64
	ObjectKey             string
	RequestedQty          units.Qty
	TrackedQty            units.Qty
	ExternalQty           units.Qty
}

func (t *ledgerTx) InsertExternalControlEpisode(episode ExternalControlEpisode) error {
	if episode.ID == "" || episode.OperationID == "" || episode.BrokerObservationID == "" ||
		episode.ObservationGeneration <= 0 || episode.ObjectKey == "" {
		return fmt.Errorf("external control episode identity is incomplete")
	}
	if episode.Origin != "external" && episode.Origin != "ambiguous" && episode.Origin != "mixed" {
		return fmt.Errorf("external control episode origin is invalid")
	}
	switch episode.ControlAction {
	case "cancel_order":
		if episode.RequestedQty != 0 || episode.TrackedQty != 0 || episode.ExternalQty != 0 {
			return fmt.Errorf("cancel control episode has position quantity")
		}
	case "close_position":
		total, err := units.AddQty(episode.TrackedQty, episode.ExternalQty)
		if err != nil || episode.RequestedQty <= 0 || episode.TrackedQty < 0 || episode.ExternalQty < 0 ||
			total != episode.RequestedQty {
			return fmt.Errorf("close control episode quantity split is invalid")
		}
	default:
		return fmt.Errorf("external control episode action is invalid")
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO external_control_episode
		(id,operation_id,control_action,origin,broker_observation_id,
		 observation_generation,object_key,requested_qty,tracked_qty,external_qty)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, episode.ID, episode.OperationID,
		episode.ControlAction, episode.Origin, episode.BrokerObservationID,
		episode.ObservationGeneration, episode.ObjectKey, int64(episode.RequestedQty),
		int64(episode.TrackedQty), int64(episode.ExternalQty))
	return normalizeDBError(err)
}
