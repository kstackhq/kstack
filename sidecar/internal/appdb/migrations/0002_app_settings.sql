-- app_settings: the app-level choices the user makes in Kstack's own UI, one
-- typed column each, in the table's one row: slot is always 0, and this
-- migration inserts it. A new setting is a new column.
--
-- default_cluster_id is Kstack's own default cluster, which sits above the
-- kubeconfig's current-context and never writes it. Deleting the cluster's row
-- clears it; a default that names a disabled or archived cluster stays, and
-- readers fall back to the kubeconfig's.
CREATE TABLE app_settings (
  slot               INTEGER PRIMARY KEY CHECK (slot = 0),
  default_cluster_id TEXT    REFERENCES clusters(id) ON DELETE SET NULL
) STRICT;

INSERT INTO app_settings (slot) VALUES (0);
