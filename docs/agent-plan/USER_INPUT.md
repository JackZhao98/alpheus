# User Query, Intent, and Confirmation

> Status: **FROZEN ARCHITECTURE — the intake, interpretation, interruption, and
> confirmation boundaries are authoritative. Exact schemas, UI transport, and
> implementation acceptance are not yet specified or authorized.**

## Purpose

Users communicate naturally, while the Agent Platform operates on durable,
typed requests. LLM assistance is necessary to interpret conversational,
multilingual, contextual, and ambiguous input, but an LLM interpretation is a
draft rather than a command or authorization.

## Conversation, request, and run

- `Conversation` is the long-lived user-facing thread.
- `UserRequest` is one durable user input, correction, answer, or command.
- `Run` is a bounded workflow created to serve a request, schedule, or event.

A Conversation may contain many requests and Runs. New user text is always
persisted before interpretation and never exists only inside an LLM transcript.

Every request binds at least its raw input, authenticated subject, Conversation,
attachments and checksums, creation time, referenced objects, explicit scope
and constraints, and any pending question or confirmation presented to the
user.

## Intake pipeline

```text
User input
  -> deterministic Input Gateway
  -> LLM Intent Interpreter
  -> validated IntentDraft
  -> deterministic Policy Resolver
  -> create/modify/wait/cancel/clarify
```

### Deterministic Input Gateway

Establishes facts that do not require semantic inference:

- authenticated user and permissions;
- Conversation, Run, Task, question, and confirmation identifiers;
- attachment identity through the AP0 `BlobRef`, type, size, checksum, and
  authorized storage reference; no second attachment-content identity exists;
- explicit structured controls such as cancel, pause, resume, approve, reject;
- whether a critical action is pending and whether its displayed revision is
  still current.

### LLM Intent Interpreter

Produces a typed `IntentDraft` describing, at minimum:

- new request, continuation, additional context, clarification answer,
  correction, pause, resume, cancel, approval intent, or rejection intent;
- objective, referenced objects, requested effects, scope, constraints, and
  candidate Skills;
- whether the input adds to or replaces unfinished work;
- multiple separable intents where present;
- missing information, ambiguity, and confidence with reasons.

The Interpreter is a restricted system component, not an investment Agent. It
does not create Tasks, select trades, invoke business tools, or authorize an
effect.

### Deterministic Policy Resolver

Validates the draft against real state and policy:

- referenced objects exist and belong to the authenticated subject;
- requested transition is legal for the current Run/Task state;
- selected Skills and actions are installed and permitted;
- a claimed answer corresponds to an outstanding question;
- a correction cannot rewrite committed history;
- ambiguity is acceptable for a reversible discussion step;
- critical, external, or money-related effects resolve exactly one legal
  structured authority route. A user-originated one-operation trade uses exact
  confirmation; an autonomous schedule/event route uses a current Delegation
  grant and never fabricates a user receipt.

Invalid or materially ambiguous input produces a focused clarification rather
than an inferred action.

## Mid-run user input

- Answer to an outstanding question resumes the bound waiting Task.
- Additional context becomes a new immutable Artifact available to relevant,
  not-yet-frozen Tasks.
- Correction supersedes affected unstarted work and creates a new revision;
  earlier Artifacts remain historical.
- Pause prevents admission of new work while preserving state.
- Cancel stops cancellable Agent work; it cannot erase a submitted Kernel
  operation or claim a real external effect was undone.
- Unrelated input creates a separate Run inside the Conversation.

A new message never silently changes the objective of a currently executing
Task. The Control Plane records whether it was applied, deferred, or caused a
new revision.

## Confirmation boundary

Natural-language confidence cannot authorize money or another critical effect.
When policy requires human approval, that approval must bind a structured
confirmation object containing the exact
target, revision/hash, authenticated account and user, material parameters,
risk envelope where applicable, display time, and expiry.

A short response such as `好`, `可以`, or `确认` is valid only when the user
interface binds it to exactly one still-current confirmation object. If there
are multiple candidates, a changed ticket, or no binding, the platform asks
for clarification. Any material ticket change invalidates the old confirmation.

For trading, the user confirms a Kernel-owned operation/review ticket. Intent
interpretation cannot invent quantity, order type, price, expiry, account, or
risk terms and cannot substitute for Kernel review.

This is the exact-confirmation route, not a requirement that every qualified
autonomous trade receive a human click. Scheduled/event autonomous proposals do
not pass through User Input for order authority; they require the separate
Delegation and Kernel Gate path.

Exact confirmation is a one-ticket authority path under `DELEGATION.md`; it is
not a GRACE score change or a general autonomous grant. Confirming one operation
cannot raise an Agent/Strategy capability template, approve later proposals,
or waive Kernel invariants.

For trading, `DELEGATION_POLICY.md` freezes the Kernel-owned
OperationConfirmationTicket and the dedicated User Authority Gateway-owned
TicketDisplayReceipt and ConfirmationReceipt, exclusive authority route,
expiry/consumption sequence, and separation from grant activation, breaker
resume, canary widening, unknown-effect resolution, and emergency reduction.
Ordinary Input Gateway, Web, Agent Runtime, Workers, and CI have no receipt-write
or receipt-signing credential. This file remains authoritative for
conversational binding and ambiguity behavior.

## Attachments and external text

Attachments are immutable source Artifacts with provenance and checksums. They
are parsed into bounded, addressable sections and retrieved for relevant Tasks;
they are not injected wholesale into every Session. Extracted content remains
untrusted data and cannot override system, Skill, user, Kernel, GRACE, or
Delegation policy.

## Audit and replay

Persist the raw input, resolved intent, Interpreter model/prompt/Contract
revision, ambiguity and clarification, Policy Resolver decision, applied Run or
Task transitions, and any confirmation binding. The raw user request remains
authoritative; an LLM-produced IntentDraft never replaces it.

## Required later specification

Before general User Input implementation, freeze the `UserRequest`,
`IntentDraft`, question, interrupt, and supersession state machines plus the
non-trading confirmation schemas. Trading confirmation's money-authority subset
is already frozen by `DELEGATION_POLICY.md`. Add acceptance probes for ambiguous
acknowledgement, multiple pending confirmations, stale revisions, mid-run
correction, duplicate delivery, attachment injection, and cancellation after
Kernel submission.
