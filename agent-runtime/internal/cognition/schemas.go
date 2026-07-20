package cognition

func object(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func stringSchema() map[string]any { return map[string]any{"type": "string"} }

func stringArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": stringSchema()}
}

func freeObjectSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func exitPlanSchema() map[string]any {
	return object(map[string]any{
		"stop": stringSchema(), "invalidation": stringSchema(),
		"time_stop": stringSchema(), "target": stringSchema(),
	}, "stop", "invalidation", "time_stop", "target")
}

func proposedOperationSchema() map[string]any {
	properties := map[string]any{
		"action":              map[string]any{"type": "string", "enum": []string{"open", "close", "cancel", "tighten_stop"}},
		"kind":                map[string]any{"type": "string", "enum": []string{"", "equity", "option"}},
		"underlying":          stringSchema(),
		"symbol":              stringSchema(),
		"side":                map[string]any{"type": "string", "enum": []string{"", "buy", "sell"}},
		"qty":                 map[string]any{"type": "number", "exclusiveMinimum": 0},
		"limit":               map[string]any{"type": "number", "exclusiveMinimum": 0},
		"max_risk_usd":        map[string]any{"type": "number", "minimum": 0},
		"short":               map[string]any{"type": "boolean"},
		"plan":                exitPlanSchema(),
		"thesis":              stringSchema(),
		"setup":               stringSchema(),
		"shadow":              map[string]any{"type": "boolean"},
		"broker_order_id":     stringSchema(),
		"closes_operation_id": stringSchema(),
	}
	schema := object(properties, "action", "kind", "underlying", "symbol", "side", "short", "thesis", "setup", "shadow")
	schema["allOf"] = []any{
		map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]any{"const": "open"}}}, "then": map[string]any{"required": []string{"qty", "plan"}, "properties": map[string]any{"kind": map[string]any{"enum": []string{"equity", "option"}}, "side": map[string]any{"enum": []string{"buy", "sell"}}}}},
		map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]any{"const": "close"}}}, "then": map[string]any{"required": []string{"qty"}}},
		map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]any{"const": "cancel"}}}, "then": map[string]any{"required": []string{"broker_order_id"}}},
		map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]any{"const": "tighten_stop"}}}, "then": map[string]any{"required": []string{"plan"}}},
	}
	return schema
}

func schemaFor(name string) (map[string]any, bool) {
	var schema map[string]any
	switch name {
	case "QueryIntent":
		schema = object(map[string]any{
			"route":                 map[string]any{"type": "string", "enum": []string{"SCOUT", "TEAM", "REFUSE"}},
			"objective":             stringSchema(),
			"required_capabilities": stringArraySchema(),
			"missing_inputs":        stringArraySchema(),
		}, "route", "objective", "required_capabilities", "missing_inputs")
	case "DeskDecision":
		schema = object(map[string]any{
			"action":           map[string]any{"type": "string", "enum": []string{"PROPOSE", "WAIT", "PASS"}},
			"reasoning":        stringSchema(),
			"proposals":        map[string]any{"type": "array", "items": proposedOperationSchema()},
			"watch_triggers":   stringArraySchema(),
			"blackboard_patch": freeObjectSchema(),
		}, "action", "reasoning", "proposals", "watch_triggers", "blackboard_patch")
	case "OpportunityBrief":
		schema = object(map[string]any{
			"action":           map[string]any{"type": "string", "enum": []string{"DISPATCH", "WATCH", "PASS"}},
			"candidates":       map[string]any{"type": "array", "items": freeObjectSchema()},
			"structural_notes": stringArraySchema(),
		}, "action", "candidates", "structural_notes")
	case "ExitAction":
		schema = object(map[string]any{
			"operations": map[string]any{"type": "array", "items": proposedOperationSchema()},
			"blotter":    stringSchema(),
		}, "operations", "blotter")
	case "JournalReview":
		schema = object(map[string]any{
			"outcomes":              map[string]any{"type": "array", "items": freeObjectSchema()},
			"lessons":               map[string]any{"type": "array", "items": freeObjectSchema()},
			"parameter_suggestions": stringArraySchema(),
		}, "outcomes", "lessons", "parameter_suggestions")
	default:
		return nil, false
	}
	return schema, true
}
