-- +goose Up
-- What SNMP storage checks read of their device (check.StorageDevice, as
-- JSON): its volumes, pools, disks and system. At most one row per monitor
-- in each five minutes (bucket: their start), the last check's.
CREATE TABLE storage_samples (
    monitor_id BIGINT NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    bucket     BIGINT NOT NULL,
    at         BIGINT NOT NULL,
    data       TEXT   NOT NULL,
    PRIMARY KEY (monitor_id, bucket)
);
CREATE INDEX storage_samples_bucket ON storage_samples (bucket);

-- +goose Down
DROP TABLE storage_samples;
