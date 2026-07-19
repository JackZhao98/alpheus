-- AP0-2 defines transport contracts only and owns no database tables.
-- AP0-3 must declare reviewed per-owner roles in its security/delivery pack
-- before any Agent Platform migration or handler can land.
DO $$
BEGIN
    IF to_regclass('public.agent_platform_common') IS NOT NULL THEN
        RAISE EXCEPTION 'AP0-2 common pack must not own runtime tables';
    END IF;
END
$$;
