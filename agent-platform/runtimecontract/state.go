package runtimecontract

var transitionGraph = map[RuntimeSubject]map[string]map[string]struct{}{
	SubjectRun: {
		string(RunQueued):    states(RunRunning, RunCanceled, RunSuperseded, RunDeadLettered),
		string(RunRunning):   states(RunWaiting, RunCanceling, RunSucceeded, RunFailed, RunDeadLettered),
		string(RunWaiting):   states(RunRunning, RunCanceling, RunFailed, RunCanceled, RunSuperseded, RunDeadLettered),
		string(RunCanceling): states(RunCanceled, RunFailed, RunDeadLettered),
	},
	SubjectTask: {
		string(TaskBlocked):         states(TaskReady, TaskCanceled, TaskSuperseded, TaskDeadLettered),
		string(TaskReady):           states(TaskRunning, TaskCanceled, TaskSuperseded, TaskDeadLettered),
		string(TaskRunning):         states(TaskWaiting, TaskResultCommitted, TaskFailed, TaskCanceled, TaskSuperseded, TaskDeadLettered),
		string(TaskWaiting):         states(TaskReady, TaskRunning, TaskFailed, TaskCanceled, TaskSuperseded, TaskDeadLettered),
		string(TaskResultCommitted): states(TaskSucceeded, TaskCanceled, TaskSuperseded, TaskDeadLettered),
	},
	SubjectSession: {
		string(SessionOpen): states(SessionClosed),
	},
	SubjectAttempt: {
		string(AttemptLeased):    states(AttemptExecuting, AttemptTimedOut, AttemptCanceled, AttemptSuperseded),
		string(AttemptExecuting): states(AttemptResultCommitted, AttemptFailed, AttemptTimedOut, AttemptCanceled, AttemptSuperseded),
	},
	SubjectTurn: {
		string(TurnPlanned):    states(TurnDispatched, TurnCanceled),
		string(TurnDispatched): states(TurnResultCommitted, TurnFailed, TurnUnknown, TurnCanceled),
		string(TurnUnknown):    states(TurnResultCommitted, TurnFailed),
	},
	SubjectBudget: {
		string(BudgetOpen):      states(BudgetExhausted, BudgetClosed),
		string(BudgetExhausted): states(BudgetClosed),
	},
	SubjectIntent: {},
}

func CanTransition(subject RuntimeSubject, from, to string) bool {
	fromStates, ok := transitionGraph[subject]
	if !ok {
		return false
	}
	toStates, ok := fromStates[from]
	if !ok {
		return false
	}
	_, ok = toStates[to]
	return ok
}

func states[T ~string](values ...T) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[string(value)] = struct{}{}
	}
	return result
}

func knownTriggerKind(value TriggerKind) bool {
	switch value {
	case TriggerSchedule, TriggerKernelEvent, TriggerExternalEvent, TriggerSystemMaintenance:
		return true
	default:
		return false
	}
}

func knownRunState(value RunState) bool {
	switch value {
	case RunQueued, RunRunning, RunWaiting, RunCanceling, RunSucceeded,
		RunFailed, RunCanceled, RunSuperseded, RunDeadLettered:
		return true
	default:
		return false
	}
}

func terminalRunState(value RunState) bool {
	switch value {
	case RunSucceeded, RunFailed, RunCanceled, RunSuperseded, RunDeadLettered:
		return true
	default:
		return false
	}
}

func knownTaskState(value TaskState) bool {
	switch value {
	case TaskBlocked, TaskReady, TaskRunning, TaskWaiting, TaskResultCommitted,
		TaskSucceeded, TaskFailed, TaskCanceled, TaskSuperseded, TaskDeadLettered:
		return true
	default:
		return false
	}
}

func terminalTaskState(value TaskState) bool {
	switch value {
	case TaskSucceeded, TaskFailed, TaskCanceled, TaskSuperseded, TaskDeadLettered:
		return true
	default:
		return false
	}
}

func knownSessionState(value SessionState) bool {
	return value == SessionOpen || value == SessionClosed
}

func knownAttemptState(value AttemptState) bool {
	switch value {
	case AttemptLeased, AttemptExecuting, AttemptResultCommitted, AttemptFailed,
		AttemptTimedOut, AttemptCanceled, AttemptSuperseded:
		return true
	default:
		return false
	}
}

func terminalAttemptState(value AttemptState) bool {
	switch value {
	case AttemptResultCommitted, AttemptFailed, AttemptTimedOut, AttemptCanceled, AttemptSuperseded:
		return true
	default:
		return false
	}
}

func knownTurnState(value TurnState) bool {
	switch value {
	case TurnPlanned, TurnDispatched, TurnResultCommitted, TurnFailed, TurnUnknown, TurnCanceled:
		return true
	default:
		return false
	}
}

func knownBudgetScope(value BudgetScope) bool {
	return value == BudgetRun || value == BudgetTask
}

func knownBudgetState(value BudgetState) bool {
	return value == BudgetOpen || value == BudgetExhausted || value == BudgetClosed
}

func knownCancellationMode(value CancellationMode) bool {
	return value == CancellationCancel || value == CancellationSupersede
}

func knownCancellationTarget(value CancellationTarget) bool {
	return value == CancellationRun || value == CancellationTask
}

func knownRecoveryDecision(value RecoveryDecision) bool {
	switch value {
	case RecoveryReuseCommittedResult, RecoveryRetrySameTask, RecoveryDeadLetter, RecoveryCanceled:
		return true
	default:
		return false
	}
}

func knownRuntimeSubject(value RuntimeSubject) bool {
	switch value {
	case SubjectRun, SubjectTask, SubjectSession, SubjectAttempt, SubjectTurn, SubjectBudget, SubjectIntent:
		return true
	default:
		return false
	}
}

func knownFinishReason(value ModelFinishReason) bool {
	switch value {
	case FinishStop, FinishToolUse, FinishLength, FinishContentFilter:
		return true
	default:
		return false
	}
}

func knownSubjectState(subject RuntimeSubject, state string) bool {
	switch subject {
	case SubjectRun:
		return knownRunState(RunState(state))
	case SubjectTask:
		return knownTaskState(TaskState(state))
	case SubjectSession:
		return knownSessionState(SessionState(state))
	case SubjectAttempt:
		return knownAttemptState(AttemptState(state))
	case SubjectTurn:
		return knownTurnState(TurnState(state))
	case SubjectBudget:
		return knownBudgetState(BudgetState(state))
	case SubjectIntent:
		return state == string(PublicationDisabled)
	default:
		return false
	}
}

func initialSubjectState(subject RuntimeSubject, state string) bool {
	switch subject {
	case SubjectRun:
		return state == string(RunQueued)
	case SubjectTask:
		return state == string(TaskBlocked) || state == string(TaskReady)
	case SubjectSession:
		return state == string(SessionOpen)
	case SubjectAttempt:
		return state == string(AttemptLeased)
	case SubjectTurn:
		return state == string(TurnPlanned)
	case SubjectBudget:
		return state == string(BudgetOpen)
	case SubjectIntent:
		return state == string(PublicationDisabled)
	default:
		return false
	}
}
