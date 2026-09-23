## 2024-05-24 - Missing Session ID Validation
**Vulnerability:** Path traversal and injection risk due to unvalidated `session_id` path parameter in `sensor_handlers`, `sensor_timeline_handlers`, `cost_handlers`, and `feedback_handlers`.
**Learning:** Even though `isValidSessionID` and `sanitizeIdentifier` exist, they were not applied consistently across all handlers taking `session_id`, leaving some endpoints exposed to malicious input.
**Prevention:** Enforce consistent use of both `sanitizeIdentifier()` and `isValidSessionID()` for any path parameter acting as an identifier or path component in all new and existing API handlers.

## 2024-05-24 - Missing Session ID Validation
**Vulnerability:** Path traversal and injection risk due to unvalidated `session_id` path parameter in `sensor_handlers`, `sensor_timeline_handlers`, `cost_handlers`, and `feedback_handlers`.
**Learning:** Even though `isValidSessionID` and `sanitizeIdentifier` exist, they were not applied consistently across all handlers taking `session_id`, leaving some endpoints exposed to malicious input.
**Prevention:** Enforce consistent use of both `sanitizeIdentifier()` and `isValidSessionID()` for any path parameter acting as an identifier or path component in all new and existing API handlers.
