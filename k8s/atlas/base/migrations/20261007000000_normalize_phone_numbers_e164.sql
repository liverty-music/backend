-- Normalize stored applicant / ticket-holder phone numbers to E.164
-- (modernize-protobuf-workflow, design D7).
--
-- Rule (mirrors the frontend toE164 normalizer): strip spaces, hyphens and
-- parentheses; a Japanese domestic number (0 + 9-10 digits) becomes +81 + the
-- digits without the leading 0; an E.164 number is kept without separators.
-- Rows that fit neither form are left unchanged on purpose, so the CHECK
-- constraint added by the next migration fails the deploy instead of hiding
-- them.

UPDATE ticket_applications AS t
SET applicant_phone_number = n.e164
FROM (
    SELECT id,
           CASE
               WHEN c ~ '^0[0-9]{9,10}$' THEN '+81' || substr(c, 2)
               ELSE c
           END AS e164
    FROM (
        SELECT id, regexp_replace(applicant_phone_number, '[[:space:]()-]', '', 'g') AS c
        FROM ticket_applications
    ) AS s
) AS n
WHERE t.id = n.id
  AND n.e164 ~ '^\+[1-9][0-9]{1,14}$'
  AND t.applicant_phone_number IS DISTINCT FROM n.e164;

UPDATE tickets AS t
SET holder_phone_number = n.e164
FROM (
    SELECT id,
           CASE
               WHEN c ~ '^0[0-9]{9,10}$' THEN '+81' || substr(c, 2)
               ELSE c
           END AS e164
    FROM (
        SELECT id, regexp_replace(holder_phone_number, '[[:space:]()-]', '', 'g') AS c
        FROM tickets
    ) AS s
) AS n
WHERE t.id = n.id
  AND n.e164 ~ '^\+[1-9][0-9]{1,14}$'
  AND t.holder_phone_number IS DISTINCT FROM n.e164;
