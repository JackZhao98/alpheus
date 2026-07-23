-- Moody Blues needs a bounded, credential-free view of collection coverage.
-- The GEXBOT Provider remains the only caller; neither Cortex nor Research
-- Gateway receives table access, raw payloads, or Provider credentials.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE OR REPLACE FUNCTION research.gexbot_collection_status()
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,research,platform_security
SET timezone='UTC' AS $$
DECLARE invoker RECORD; series JSONB;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.gexbot_provider_identity();
  SELECT COALESCE(jsonb_agg(jsonb_build_object(
    'symbol',candidate.symbol,
    'category',candidate.category,
    'available',latest.observation_id IS NOT NULL,
    'observations',stats.observations,
    'latest_observed_at',latest.observed_at,
    'latest_available_at',latest.available_at
  ) ORDER BY candidate.category),'[]'::JSONB) INTO series
  FROM (VALUES ('SPX'::TEXT,'gex_full'::TEXT),('SPX'::TEXT,'gex_zero'::TEXT),('SPX'::TEXT,'gex_one'::TEXT)) AS candidate(symbol,category)
  LEFT JOIN LATERAL (
    SELECT observation_id,observed_at,available_at
    FROM research.gexbot_observation
    WHERE symbol=candidate.symbol AND category=candidate.category
    ORDER BY available_at DESC,observed_at DESC,observation_id DESC
    LIMIT 1
  ) AS latest ON true
  LEFT JOIN LATERAL (
    SELECT count(*)::BIGINT AS observations
    FROM research.gexbot_observation
    WHERE symbol=candidate.symbol AND category=candidate.category
  ) AS stats ON true;
  RETURN jsonb_build_object(
    'schema_revision',1,
    'provider','gexbot_classic',
    'collection_policy_revision','gexbot_spx_30s_et_v1',
    'observation_resolution','30s',
    'series',series
  );
END $$;

REVOKE ALL ON FUNCTION research.gexbot_collection_status() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION research.gexbot_collection_status() TO alpheus_gexbot_provider;
RESET ROLE;
