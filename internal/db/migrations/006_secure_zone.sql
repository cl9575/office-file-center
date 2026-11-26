ALTER TABLE nodes ADD COLUMN space TEXT NOT NULL DEFAULT 'main' CHECK (space IN ('main', 'secure'));
ALTER TABLE nodes ADD COLUMN created_by_id TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN secure_owner_role TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN visibility_mode TEXT NOT NULL DEFAULT 'default' CHECK (visibility_mode IN ('default', 'custom'));

DROP INDEX ux_nodes_active_name;
CREATE UNIQUE INDEX ux_nodes_active_name ON nodes(space, parent_id, name) WHERE deleted_at IS NULL;
CREATE INDEX idx_nodes_space_parent ON nodes(space, parent_id) WHERE deleted_at IS NULL;

CREATE TABLE node_acl (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    principal_type TEXT NOT NULL CHECK (principal_type IN ('role', 'user')),
    principal_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    created_by TEXT NOT NULL,
    PRIMARY KEY (node_id, principal_type, principal_id)
);

CREATE INDEX idx_node_acl_principal ON node_acl(principal_type, principal_id);

ALTER TABLE audit ADD COLUMN secure INTEGER NOT NULL DEFAULT 0;

INSERT INTO nodes
    (id, parent_id, name, kind, current_version_id, revision, created_at, created_by,
     updated_at, updated_by, space, created_by_id, secure_owner_role, visibility_mode)
SELECT lower(hex(randomblob(16))), '', '权限区', 'folder', '', 0,
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), '系统',
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), '系统', 'secure', '', '', 'default'
WHERE NOT EXISTS (
    SELECT 1 FROM nodes
    WHERE space = 'secure' AND parent_id = '' AND kind = 'folder' AND deleted_at IS NULL
);
