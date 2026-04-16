---
paths:
  - "**/*.go"
---
# Go Mutex Discipline

Every `Lock()`/`RLock()` is immediately followed by `defer Unlock()`/`defer RUnlock()` on the next line. For early-release cases (release before slow work, multiple critical sections in one function), wrap each critical section in an IIFE so `defer` still applies. Bare `Unlock()` is never the right answer — the IIFE pattern handles every case the bare pattern was reaching for, with panic safety.
