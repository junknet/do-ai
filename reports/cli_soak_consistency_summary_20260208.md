# CLI Soak Consistency Summary (2026-02-08)

- log: reports/cli_soak_consistency_20260208.log
- auto_inject_count: 1
- throttle_hits: 1
- submit_send_count: 1
- fallback_send_count: 1
- submit_mode_target_hits(mode=enter,target=codex): 2
- panic_like_count: 0

## Result
- pass_if: submit_send_count>0 AND panic_like_count=0
- observed: PASS
