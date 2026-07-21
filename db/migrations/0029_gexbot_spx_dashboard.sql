-- The public SPX dashboard records the three Classic aggregations on the same
-- fixed cadence.  Collection policy is code-owned for this narrowly scoped
-- product, not a browser setting.
ALTER TABLE gexbot_observation DROP CONSTRAINT gexbot_observation_category_check;
ALTER TABLE gexbot_observation ADD CONSTRAINT gexbot_observation_category_check
  CHECK (category IN ('gex_full','gex_zero','gex_one'));

ALTER TABLE gexbot_observation
  ADD COLUMN major_pos_vol NUMERIC,
  ADD COLUMN major_pos_oi NUMERIC,
  ADD COLUMN major_neg_vol NUMERIC,
  ADD COLUMN major_neg_oi NUMERIC;
