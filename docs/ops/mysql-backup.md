# MySQL Backup SOP

When handling MySQL backup/restore:

1. Verify disk space before backup.
2. Run physical backup with a consistent method (e.g. xtrabackup/mysqldump).
3. Validate backup integrity and run a restore drill in staging.
4. Record backup timestamp, binlog position, and retention policy.
