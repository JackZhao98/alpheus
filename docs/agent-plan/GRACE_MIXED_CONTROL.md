# GRACE Mixed-Control Attribution

[Back to Agent Plan Index](INDEX.md)

> Status: **FROZEN ARCHITECTURE AMENDMENT — exact schemas, estimator choices,
> Calibration Pack, and independent model-risk acceptance remain required**
>
> Scope: delayed GRACE evaluation when an Alpheus decision and a human/external
> broker action share control of one order, position, or economic outcome. This
> document does not give GRACE broker or authorization power.

## 1. Decision

Human intervention does not erase a trade and does not make its whole outcome
either "AI PnL" or "human PnL." GRACE separates four questions:

1. What actually happened economically in the broker account?
2. Who controlled each quantity and time segment?
3. Which Agent behavior was committed before the outcome and remains
   objectively scoreable?
4. Which later path is only counterfactual or unidentified?

Actual reconciled PnL, fees, fills, and position changes are recorded once and
at full value. They are never multiplied by a generic intervention discount.
Uncertainty changes attribution strength and scoreability, not broker history.

```text
one reconciled economic outcome
              |
      ordered control episodes
     /          |           \
 entry       management      exit
     \          |           /
 behavior-specific evaluation
              |
  explicit attributable / censored /
       uncertain evidence states
```

GRACE remains delayed evaluation. It publishes evidence and credibility
snapshots. Delegation may consume an eligible snapshot under its separately
frozen policy; Kernel alone enforces actual authority and risk.

## 2. Ground-truth layers

### 2.1 Economic truth

The canonical account ledger records each realized broker outcome exactly once,
including all actual fills, fees, cash movement, realized PnL, and later
reconciliation corrections. Its identity remains the Position/EconomicSet and
executed decision-pipeline lineage, with a mixed-control manifest when control
changed.

The full account result is visible in account- and pipeline-level economics. It
is not copied into every Agent score, divided equally among participants, or
rewritten because a later narrative says the human rescued or harmed the trade.

### 2.2 Control truth

Kernel broker reconciliation supplies immutable external facts and ordered
control episodes. An episode identifies:

```text
economic_set_id and position/order references
effective start/end and broker observation references
actor class: alpheus_agent | human | kernel_safety | external_unknown
action/intent reference, if one existed before the effect
exact quantity or quantity range controlled
entry | management | exit | cancel | size_change | unknown role
origin and identity confidence
supersession/reconciliation generation
```

Control is segmented by both quantity and time. A human sale of 30 shares from
a 100-share position changes control for those 30 shares at that fill; it does
not retroactively make the prior 70-share history human-controlled or pretend
the remaining 70 shares were sold.

When broker data cannot identify which economic lots were affected, Kernel may
use a conservative accounting allocation for safety, but GRACE records
`ATTRIBUTION_UNCERTAIN`. An accounting convention is not causal evidence.

### 2.3 Behavior truth

GRACE evaluates only immutable behaviors registered before the outcome:
forecast, entry decision, WAIT/PASS, management recommendation, exit decision,
monitoring obligation, invalidation, or challenge. Each behavior retains its
original horizon, comparator, required facts, confidence, and evaluation
profile.

No Post Mortem, human explanation, or Coach summary may create a missing prior
decision or change who controlled an episode.

### 2.4 Counterfactual truth

After a human intervention, "what the AI would have done" is not an actual
outcome. It may be recorded only when the relevant policy and monitoring loop
continued under a frozen Shadow/counterfactual protocol. It remains separately
labeled and cannot be converted into actual Live PnL or used to assign certain
causal blame.

## 3. Intervention classification

Classification uses pre-effect records and broker facts, never the final PnL.

### 3.1 Human executes an existing AI intent

Use `HUMAN_EXECUTOR_OF_PRIOR_INTENT` only when an immutable Agent decision:

- predates the human broker action;
- names the same bound account, instrument, direction, and compatible quantity;
- remains inside its declared validity/freshness window; and
- has no material price, order-type, or risk-envelope mismatch.

The Agent may receive decision-quality evidence for the entry/exit it actually
recommended. The broker effect remains external in Kernel, and execution
quality is not credited to the Agent where the human selected materially
different execution terms.

Similarity discovered after the fill is insufficient. If no exact prior intent
exists, the action is an independent intervention.

### 3.2 Independent human intervention

`INDEPENDENT_HUMAN_INTERVENTION` begins when the human changes quantity,
direction, order state, or exit timing without an exact current Agent intent.
Actual economics remain complete, while affected Agent management/exit targets
are evaluated only up to the intervention or censored under their frozen
profiles.

### 3.3 Proven emergency intervention

`PROVEN_EMERGENCY_INTERVENTION` is not a user-selected label. It requires
objective evidence that, before the human effect:

- a predeclared Agent/Strategy stop, invalidation, monitoring deadline, or
  mandatory escalation was due;
- the required data and permitted action were available;
- no valid superseding decision or Kernel safety block explains the omission;
  and
- the human action reduced the exact exposed risk.

Only then may the missed obligation become adverse management/compliance
evidence for the responsible behavior. A price decline followed by a human
sale is not by itself proof that the AI should have acted earlier.

### 3.4 Unknown or conflicting control

Missing order identity, overlapping tools, delayed reconciliation, conflicting
timestamps, or an unexplained quantity delta produces
`CONTROL_ORIGIN_UNCERTAIN`. It cannot strengthen an authority-bearing score.
GRACE may retain conservative team/economic evidence and must expose the
coverage limitation.

## 4. Role and lifecycle treatment

### AI entry, human exit

- The complete realized account PnL remains in the one economic ledger with a
  mixed-control label.
- The AI entry is evaluated against its predeclared entry forecast/comparator
  and observable market path. It may receive entry evidence even though it did
  not control the exit.
- Management behavior before intervention remains scoreable where its target
  matured before that point.
- Exit/late-management responsibility after independent intervention is
  censored or uncertain, not assigned by a generic fractional weight.
- If the human executed an exact prior AI exit intent, the AI exit decision may
  be evaluated; the human remains the broker executor.
- If a proven missed obligation preceded a human rescue, that specific miss is
  adverse evidence. GRACE does not infer the miss from profit/loss alone.

### Human entry, AI exit

- The AI receives no discovery or entry credit for the human purchase.
- A later immutable AI management/exit decision may receive its own
  behavior-specific evidence from the point it assumes control.
- Actual PnL remains one mixed-control economic outcome; it is not all assigned
  to the exit Agent.

### Human exits earlier than the AI would have

The actual result remains actual. The AI is not charged the later opportunity
cost unless a frozen, identified evaluation target makes that comparison
scoreable. A continued hypothetical position is Shadow/counterfactual evidence,
not missing account PnL.

### Partial intervention or manual size change

Evaluation splits on exact reconciled quantities and episode boundaries. An
external add increases the account's actual risk but does not become an AI
entry. An external reduction stops AI control for the reduced quantity while
the remaining quantity continues under its existing control state. If exact
lot allocation is unknowable, individual economic attribution stays uncertain.

### Kernel safety action

A Kernel breaker, forced containment, or reconciliation action is its real
origin. It may provide compliance evidence about the behavior that caused the
condition, but Kernel never becomes an investment Agent and receives no market
credibility score.

## 5. Evidence states, not arbitrary weights

Every affected AtomicEvaluation publishes an explicit control/attribution
state such as:

- `FULL_ROLE_SEGMENT`: the Role controlled and predeclared the evaluated
  behavior for the exact segment;
- `HUMAN_EXECUTOR_OF_PRIOR_INTENT`: Agent decision is scoreable, external
  execution remains separately identified;
- `CENSORED_BY_INTERVENTION`: the frozen target cannot mature after control
  changed;
- `MISSED_PREDECLARED_OBLIGATION`: independently evidenced omission before a
  proven intervention;
- `ATTRIBUTION_UNCERTAIN`: economics are known but individual contribution is
  not identified; or
- `NOT_ROLE_OWNED`: the Role did not own that lifecycle decision.

There is no default `human_intervention_weight`, no universal 50% credit, and
no forced allocation summing to 100%. Any later statistical weighting for
informative censoring or causal estimation requires an identified design,
overlap diagnostics, sensitivity analysis, independent validation, and a
signed Calibration Pack. Without those prerequisites, GRACE reports segmented
descriptive evidence and uncertainty.

Human intervention is generally an informative competing event: humans are
more likely to intervene when risk, disagreement, or unusual conditions are
already present. Treating it as random censoring is forbidden.

## 6. Required machine bindings

AP8 must extend the neutral Behavior/Outcome foundation with typed equivalents
of:

- `ControlEpisode` and `ControlEpisodeRevision`;
- `InterventionClassification` plus evidence references and classifier
  revision;
- exact quantity/time segment bindings;
- prior Agent intent and monitoring-obligation references;
- economic outcome id and non-duplication key;
- control/attribution state on `AtomicEvaluation` inputs;
- censoring/uncertainty reason and affected horizon;
- reconciliation correction/supersession; and
- provenance binding to Kernel's Broker Observation/External Control Episode.

Kernel owns broker and control facts. GRACE Intake owns evaluation intake and
classification records derived from those immutable facts. GRACE Engine owns
scores. Coach may submit a candidate explanation only. None may update the
Kernel fact or its origin.

The classifier is deterministic and versioned. Ambiguous cases do not call an
LLM to choose the favorable label. An LLM may produce a Post Mortem narrative
after classification, but that prose is not a model input unless a future
separately reviewed structured feature contract proves it safe and useful.

## 7. Post Mortem behavior

Mixed-control Post Mortems stay short and evidence-linked:

```text
actual economic result and risk units
control timeline and affected quantities
which prior Agent intents/obligations existed
intervention classification and uncertainty
entry / management / exit findings kept separate
one candidate lesson, if falsifiable
```

The Coach must not say "human saved the trade" or "human sold too early"
without the objective classification evidence above. A candidate lesson earns
no immediate score and follows the normal independent validation path.

Control timelines and canonical records are retrieved on demand. They are not
appended forever to every Agent prompt. Context manifests carry the bounded
current state and references; older episodes remain queryable evidence for
GRACE/Coach, preserving the existing Memory and compaction boundary.

## 8. Model and policy ownership

Classifier profiles, maturity horizons, estimator choices, priors, thresholds,
and any permitted evidence-strength mapping are typed immutable database/model
revisions with validation and activation records. They are not prompt text,
static role YAML, or hard-coded trading policy. The small category vocabulary
and its fail-closed structural invariants belong to versioned schemas/code, not
an editable generic settings table.

Code retains structural prohibitions: one economic outcome cannot be counted
twice, future narratives cannot create prior intent, uncertain attribution
cannot strengthen authority, and actual broker facts cannot be discounted or
rewritten.

An active GRACE Champion never tunes these parameters in place. New evidence
may produce new ScoreSnapshots under the frozen Champion. Parameter/model
changes create an offline Challenger and follow the existing independent
Validator, model-risk approval, forward observe-only, and atomic promotion
process.

## 9. Delivery order

1. Kernel B0 lands broker observations, origin, external-control episodes, and
   pre-effect reconciliation before AP0.
2. AP0 freezes the shared identities/digests needed to reference those Kernel
   facts without cross-schema writes.
3. AP8 stores neutral mixed-control evidence and outcomes. No official
   authority-bearing score is published.
4. AP9 implements the independently reviewed GRACE classifier/evaluation
   profile, golden fixtures, Calibration Pack, Validator, and observe-only
   score publication.
5. Delegation may consume only compatible, mature, non-uncertain GRACE evidence
   under its own frozen policy. Kernel still independently gates every effect.

This plan does not require a new service. Under Lean v1, the records and
deterministic classifier can live in the existing credential-isolated GRACE
profiles.

## 10. Acceptance

Golden fixtures and integration probes must prove at least:

1. AI buy + exact prior AI close intent + matching human sell: actual PnL is
   recorded once; AI exit decision is scoreable; broker execution stays
   external.
2. AI buy + unplanned human sell: entry evidence remains eligible under its
   declared profile; post-intervention AI exit responsibility is censored;
   actual PnL is neither erased nor fully assigned to the AI.
3. AI buy + human rescue after a provably missed stop/monitor deadline: only
   the exact missed obligation becomes adverse evidence, with all four proof
   conditions present.
4. Same loss without a predeclared due obligation: no hindsight-generated AI
   violation or rescue label.
5. Human buy + AI sell: AI receives management/exit evidence only and no entry
   or discovery credit.
6. Partial human sale: exact quantity segments reconcile to the broker total,
   with no duplicate PnL and remaining quantity still governed correctly.
7. Human early profit-taking followed by a rally: later opportunity cost stays
   counterfactual and does not overwrite actual PnL or automatically penalize
   AI.
8. External add to an AI-origin position: extra quantity is not attributed to
   the AI entry; account risk and economic total remain complete.
9. Ambiguous identity/timestamps/lot allocation: attribution is uncertain and
   cannot improve an authority-bearing snapshot.
10. A Coach claiming rescue, early exit, or exact intent cannot alter the
    deterministic classification, prior records, or score input.
11. Replay after late broker reconciliation deterministically supersedes the
    affected classifications/snapshots without mutating history or duplicating
    economics.
12. Complete-stream reports expose intervention/censoring rates by Strategy,
    Role, regime, and outcome so selective intervention cannot silently bias a
    Champion comparison.

## 11. Explicit non-goals

- no simple profit-equals-credit or loss-equals-blame rule;
- no generic human-intervention score multiplier;
- no causal claim from an accounting allocation;
- no LLM-written attribution entering the quantitative score;
- no GRACE broker access, trade approval, grant creation, or hard-limit change;
- no real-money experiment designed to identify human-versus-AI contribution;
  and
- no promise to recover an unobservable counterfactual when control changed.
