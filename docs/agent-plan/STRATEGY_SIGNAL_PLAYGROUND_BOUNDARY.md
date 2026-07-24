# Strategy Signal / Live Loop / Paper Playground Boundary

> Status: product boundary corrected on 2026-07-24. This document supersedes
> any Console wording that treated a Trigger as a manually configured alarm or
> placed historical replay inside the live trading console.

## 1. Live market loop

The live Console observes current market data on a seconds-level cadence. A
poll produces one immutable market frame. Installed mathematical detectors
consume that same frame; they do not each invent or independently fetch a
different market state.

The chart is a projection of the frame stream. It is not itself a Trigger and
does not wake an Agent merely because the page is open.

## 2. Detector, Signal and Wake

A Detector is a versioned mathematical model or strategy function, for
example:

- MACD golden/death cross;
- volatility or volume-regime transition;
- GEX zero-gamma, wall migration or concentration pattern;
- a later normalized quantitative feature pipeline.

A Detector evaluates frames and emits a durable Signal containing its exact
version, input frame reference, feature values, result and observed time. A
Signal is authority-free evidence.

Only a reviewed Wake Policy may turn a qualifying Signal into a Cortex
Occurrence and Run. The AI does not poll continuously; it sleeps until a
Signal merits reasoning. A Detector never places an order and a Signal never
grants an effect.

The existing `Decision Trigger Registry` is therefore a first threshold
detector implementation. Its durable generations, samples and Wake boundary
remain useful, but the product name is `Signal Detector`, not a user-authored
alarm.

## 3. Paper Strategy Playground

Historical replay exists only inside a dedicated Paper Strategy Playground.
It is not part of Live mode and is not shown in the primary Command Console.

Every Playground Session freezes:

- its own initial cash and isolated Paper account;
- replay provider, symbol, series, time range and `as_of` fence;
- selected Detector IDs and exact generations;
- speed and virtual clock;
- Agent workflow/model policy and effect ceiling;
- resulting Signals, Wake Runs, Candidates, simulated fills and performance.

The Playground may later accept AI-generated strategy proposals, but generated
code cannot execute directly. It must compile into the same reviewed,
versioned Detector contract and pass deterministic replay tests first.

Playground money, positions, orders and performance never merge into the
default Paper account or Live Robinhood account.

## 4. Product surfaces

| Surface | Responsibility |
|---|---|
| Command Console | Live/Paper monitoring, current chart, installed Detector health, Signals, AI decisions, portfolio and effects |
| Strategy Playground | Historical replay, isolated starting capital, Detector selection/generation, full simulated Agent participation and performance |
| Agent Lab | Tool and routing verification only |
| Research / Moody Blues | Temporal data collection, `as_of`, replay cursors and normalized frames |

## 5. Current implementation gap

The evaluator now polls every second and supports scalar threshold/cross
detectors. The next runtime slice consolidates those reads into a
seconds-level shared-frame loop and adds richer feature detectors. Historical
replay already has deterministic frame, Signal Sample, Occurrence and Wake
evidence; it must now be presented only through the Paper Playground and bound
to an isolated account and selected Detector set.
