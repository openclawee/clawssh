# OOM Triage SOP

When OOM is suspected:

1. Capture memory pressure indicators (`free -m`, `dmesg`, process RSS).
2. Identify top memory consumers and recent deployment changes.
3. Apply short-term mitigation (restart/leak isolation) if service is degraded.
4. Plan permanent fix: memory limit tuning, leak patch, or capacity increase.
