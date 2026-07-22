-- Migration: Clear HTTP cache for v3 key format upgrade
-- v3 removes |HX=, |HXTarget=, |IsVariant= suffixes and uses normalized |Variant= only.
-- Old v2 key strings are incompatible; the entire cache must be rebuilt.

DELETE FROM http_cache;
