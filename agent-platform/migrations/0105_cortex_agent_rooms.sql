-- Product-facing durable Agent Rooms. Conversation and UserRequest facts stay
-- immutable; this projection owns only user-visible room metadata and state.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_input.cortex_agent_room (
    conversation_id TEXT PRIMARY KEY REFERENCES
        agent_input.conversation(conversation_id),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision=1),
    subject_principal_id TEXT NOT NULL CHECK (
        agent_input.input_identifier_valid(subject_principal_id)
    ),
    mode TEXT NOT NULL CHECK (mode IN (
        'research','spx_gamma','equity_discovery','watchlist_monitor'
    )),
    title TEXT NOT NULL CHECK (
        title=btrim(title)
        AND title<>''
        AND octet_length(title)<=240
        AND title!~'[[:cntrl:]]'
    ),
    state TEXT NOT NULL CHECK (state IN ('active','paused','archived')),
    generation BIGINT NOT NULL CHECK (generation>0),
    last_run_id TEXT REFERENCES agent_control.runtime_run(run_id),
    created_at TIMESTAMPTZ NOT NULL,
    last_activity_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (created_at<=last_activity_at AND last_activity_at<=updated_at)
);

CREATE FUNCTION agent_input.guard_cortex_agent_room()
RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path=pg_catalog,agent_input
SET timezone='UTC'
AS $$
BEGIN
    IF TG_OP='DELETE'
       OR NEW.conversation_id<>OLD.conversation_id
       OR NEW.schema_revision<>OLD.schema_revision
       OR NEW.subject_principal_id<>OLD.subject_principal_id
       OR NEW.created_at<>OLD.created_at
       OR NEW.generation<>OLD.generation+1
       OR NEW.updated_at<OLD.updated_at
       OR NEW.last_activity_at<OLD.last_activity_at
       OR OLD.state='archived' THEN
        RAISE EXCEPTION USING ERRCODE='55000',
            MESSAGE='invalid Cortex Agent Room mutation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER cortex_agent_room_guard
BEFORE UPDATE OR DELETE ON agent_input.cortex_agent_room
FOR EACH ROW EXECUTE FUNCTION agent_input.guard_cortex_agent_room();

CREATE FUNCTION agent_input.cortex_agent_room_json(
    p_room agent_input.cortex_agent_room
) RETURNS JSONB
LANGUAGE sql
STABLE
SET search_path=pg_catalog,agent_input,agent_control
SET timezone='UTC'
AS $$
    SELECT jsonb_strip_nulls(jsonb_build_object(
        'conversation_id',p_room.conversation_id,
        'conversation_created_at',
            agent_control.runtime_utc_text(p_room.created_at),
        'mode',p_room.mode,
        'title',p_room.title,
        'state',p_room.state,
        'generation',p_room.generation,
        'last_run_id',p_room.last_run_id,
        'last_run_state',run.state,
        'last_activity_at',
            agent_control.runtime_utc_text(p_room.last_activity_at),
        'updated_at',agent_control.runtime_utc_text(p_room.updated_at),
        'message_count',(
            SELECT count(*)
            FROM agent_input.user_request AS request
            WHERE request.conversation_id=p_room.conversation_id
        )
    ))
    FROM (SELECT 1) AS present
    LEFT JOIN agent_control.runtime_run AS run
      ON run.run_id=p_room.last_run_id
$$;

CREATE FUNCTION agent_input.record_cortex_agent_room(
    p_subject_principal_id TEXT,
    p_conversation_id TEXT,
    p_mode TEXT,
    p_title TEXT,
    p_run_id TEXT
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_input,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    conversation_row agent_input.conversation%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    room_row agent_input.cortex_agent_room%ROWTYPE;
    at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_input.input_identifier_valid(p_subject_principal_id)
       OR NOT agent_input.input_identifier_valid(p_conversation_id)
       OR NOT agent_control.runtime_identifier_valid(p_run_id)
       OR p_mode NOT IN (
            'research','spx_gamma','equity_discovery','watchlist_monitor'
       )
       OR p_title IS NULL OR p_title<>btrim(p_title)
       OR p_title='' OR octet_length(p_title)>240
       OR p_title~'[[:cntrl:]]' THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex Agent Room record denied';
    END IF;

    SELECT * INTO conversation_row
    FROM agent_input.conversation
    WHERE conversation_id=p_conversation_id
      AND subject_principal_id=p_subject_principal_id
    FOR SHARE;
    SELECT * INTO run_row
    FROM agent_control.runtime_run
    WHERE run_id=p_run_id
      AND origin_kind='user_request'
      AND origin_conversation_record_id=p_conversation_id
      AND origin_initiating_principal_id=p_subject_principal_id
    FOR SHARE;
    IF conversation_row.conversation_id IS NULL OR run_row.run_id IS NULL THEN
        RETURN jsonb_build_object(
            'status','denied','reason_code','agent_room_target_not_found'
        );
    END IF;

    SELECT * INTO room_row
    FROM agent_input.cortex_agent_room
    WHERE conversation_id=p_conversation_id
    FOR UPDATE;
    IF FOUND THEN
        IF room_row.subject_principal_id<>p_subject_principal_id
           OR room_row.state='archived' THEN
            RETURN jsonb_build_object(
                'status','denied',
                'reason_code','agent_room_target_not_found'
            );
        END IF;
        IF room_row.mode<>p_mode THEN
            RETURN jsonb_build_object(
                'status','conflict',
                'reason_code','agent_room_mode_mismatch'
            );
        END IF;
        UPDATE agent_input.cortex_agent_room
        SET last_run_id=p_run_id,
            generation=room_row.generation+1,
            last_activity_at=greatest(
                room_row.last_activity_at,run_row.created_at
            ),
            updated_at=greatest(at_time,run_row.created_at)
        WHERE conversation_id=p_conversation_id
        RETURNING * INTO room_row;
    ELSE
        INSERT INTO agent_input.cortex_agent_room(
            conversation_id,schema_revision,subject_principal_id,
            mode,title,state,generation,last_run_id,
            created_at,last_activity_at,updated_at
        ) VALUES(
            p_conversation_id,1,p_subject_principal_id,
            p_mode,p_title,'active',1,p_run_id,
            conversation_row.created_at,
            greatest(conversation_row.created_at,run_row.created_at),
            greatest(at_time,run_row.created_at)
        )
        RETURNING * INTO room_row;
    END IF;
    RETURN jsonb_build_object(
        'status','recorded',
        'room',agent_input.cortex_agent_room_json(room_row)
    );
END
$$;

CREATE FUNCTION agent_input.list_cortex_agent_rooms(
    p_subject_principal_id TEXT,
    p_limit INTEGER
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_input,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_input.input_identifier_valid(p_subject_principal_id)
       OR p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex Agent Room list denied';
    END IF;
    RETURN COALESCE((
        SELECT jsonb_agg(
            agent_input.cortex_agent_room_json(room)
            ORDER BY room.last_activity_at DESC,room.conversation_id
        )
        FROM (
            SELECT selected.*
            FROM agent_input.cortex_agent_room AS selected
            WHERE selected.subject_principal_id=p_subject_principal_id
              AND selected.state<>'archived'
            ORDER BY selected.last_activity_at DESC,
                selected.conversation_id
            LIMIT p_limit
        ) AS room
    ),'[]'::JSONB);
END
$$;

CREATE FUNCTION agent_input.get_cortex_agent_room(
    p_subject_principal_id TEXT,
    p_conversation_id TEXT
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_input,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    room_row agent_input.cortex_agent_room%ROWTYPE;
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_input.input_identifier_valid(p_subject_principal_id)
       OR NOT agent_input.input_identifier_valid(p_conversation_id) THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex Agent Room read denied';
    END IF;
    SELECT * INTO room_row
    FROM agent_input.cortex_agent_room
    WHERE conversation_id=p_conversation_id
      AND subject_principal_id=p_subject_principal_id
      AND state<>'archived';
    IF NOT FOUND THEN RETURN NULL; END IF;
    RETURN agent_input.cortex_agent_room_json(room_row);
END
$$;

CREATE FUNCTION agent_input.update_cortex_agent_room(
    p_subject_principal_id TEXT,
    p_conversation_id TEXT,
    p_expected_generation BIGINT,
    p_mode TEXT,
    p_title TEXT,
    p_state TEXT
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_input,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    room_row agent_input.cortex_agent_room%ROWTYPE;
    at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_input.input_identifier_valid(p_subject_principal_id)
       OR NOT agent_input.input_identifier_valid(p_conversation_id)
       OR p_expected_generation IS NULL OR p_expected_generation<1
       OR p_mode NOT IN (
            'research','spx_gamma','equity_discovery','watchlist_monitor'
       )
       OR p_title IS NULL OR p_title<>btrim(p_title)
       OR p_title='' OR octet_length(p_title)>240
       OR p_title~'[[:cntrl:]]'
       OR p_state NOT IN ('active','paused','archived') THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex Agent Room update denied';
    END IF;
    SELECT * INTO room_row
    FROM agent_input.cortex_agent_room
    WHERE conversation_id=p_conversation_id
      AND subject_principal_id=p_subject_principal_id
    FOR UPDATE;
    IF NOT FOUND OR room_row.state='archived' THEN
        RETURN jsonb_build_object(
            'status','denied','reason_code','agent_room_target_not_found'
        );
    END IF;
    IF room_row.generation<>p_expected_generation THEN
        RETURN jsonb_build_object(
            'status','conflict',
            'reason_code','agent_room_generation_changed',
            'room',agent_input.cortex_agent_room_json(room_row)
        );
    END IF;
    UPDATE agent_input.cortex_agent_room
    SET mode=p_mode,title=p_title,state=p_state,
        generation=room_row.generation+1,
        updated_at=greatest(at_time,room_row.updated_at)
    WHERE conversation_id=p_conversation_id
    RETURNING * INTO room_row;
    RETURN jsonb_build_object(
        'status','updated',
        'room',agent_input.cortex_agent_room_json(room_row)
    );
END
$$;

REVOKE ALL ON TABLE agent_input.cortex_agent_room FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_input.guard_cortex_agent_room(),
agent_input.cortex_agent_room_json(agent_input.cortex_agent_room),
agent_input.record_cortex_agent_room(TEXT,TEXT,TEXT,TEXT,TEXT),
agent_input.list_cortex_agent_rooms(TEXT,INTEGER),
agent_input.get_cortex_agent_room(TEXT,TEXT),
agent_input.update_cortex_agent_room(TEXT,TEXT,BIGINT,TEXT,TEXT,TEXT)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_input.record_cortex_agent_room(TEXT,TEXT,TEXT,TEXT,TEXT),
agent_input.list_cortex_agent_rooms(TEXT,INTEGER),
agent_input.get_cortex_agent_room(TEXT,TEXT),
agent_input.update_cortex_agent_room(TEXT,TEXT,BIGINT,TEXT,TEXT,TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
