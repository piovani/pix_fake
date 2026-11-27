DROP TABLE med_watermarks;
DROP TABLE med_refunds;
DROP TABLE med_reports;
ALTER TABLE payments DROP COLUMN return_reason;
ALTER TABLE reservations DROP COLUMN purpose;
