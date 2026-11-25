ALTER TABLE access_groups
    ADD COLUMN leader_user_id TEXT,
    ADD CONSTRAINT access_groups_leader_membership_fk
        FOREIGN KEY (leader_user_id, id)
        REFERENCES user_group_memberships (user_id, group_id)
        DEFERRABLE INITIALLY DEFERRED;
