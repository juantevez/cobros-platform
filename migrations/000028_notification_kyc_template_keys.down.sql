-- migrations/000028_notification_kyc_template_keys.down.sql
UPDATE notification_preferences
SET event_type = CASE event_type
        WHEN 'onboarding.application.approved' THEN 'kyc.approved'
        ELSE 'kyc.rejected' END
WHERE event_type IN ('onboarding.application.approved', 'onboarding.application.rejected');
