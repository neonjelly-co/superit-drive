-- Guests sign in with a link now, whose code is long and is never typed. The six-digit codes mailed by an older version
-- cannot be used any more (the page has no field for them), and one sent just before the upgrade would keep its guest
-- from being sent a link for a few minutes, as a code "sent lately and not used yet". They go.
DELETE FROM guest_codes;
