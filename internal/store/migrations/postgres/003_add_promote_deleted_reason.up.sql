-- 1. Drop the existing constraint
ALTER TABLE scores DROP CONSTRAINT scores_deleted_reason_check;

-- 2. Add the updated constraint that includes 'promote'
ALTER TABLE scores ADD CONSTRAINT scores_deleted_reason_check 
    CHECK (deleted_reason IN ('manual', 'max_history', 'promote'));
