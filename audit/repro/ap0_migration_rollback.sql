SELECT 'ap0-agent-migration-rollback-pass' WHERE
    NOT EXISTS (
        SELECT 1 FROM pg_class AS relation
        JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
        WHERE namespace.nspname IN ('agent_control', 'blob', 'platform_governance')
          AND relation.relkind IN ('r', 'p', 'v', 'm', 'S')
    );
