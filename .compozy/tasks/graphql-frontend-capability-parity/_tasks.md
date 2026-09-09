---
schema_version: "compozy.tasks/v2"
workflow: graphql-frontend-capability-parity
graph:
  nodes:
    - id: task_01
      file: task_01.md
    - id: task_02
      file: task_02.md
    - id: task_03
      file: task_03.md
    - id: task_04
      file: task_04.md
    - id: task_05
      file: task_05.md
    - id: task_06
      file: task_06.md
    - id: task_07
      file: task_07.md
    - id: task_08
      file: task_08.md
  edges:
    - from: task_01
      to: task_02
    - from: task_02
      to: task_03
    - from: task_03
      to: task_04
    - from: task_04
      to: task_05
    - from: task_04
      to: task_06
    - from: task_04
      to: task_07
    - from: task_05
      to: task_08
    - from: task_06
      to: task_08
    - from: task_07
      to: task_08
---

# GraphQL and Frontend Capability Parity Task List

Eight robust tasks complete the backend-first contract, deliver the three product experiences, prove executable parity, and retire the legacy frontend only after the release gate passes.

| Task | Title | Type | Complexity | Assigned tests |
|---|---|---|---|---:|
| task_01 | Membership Context and Super Admin Foundation | backend | critical | 25 |
| task_02 | Administrative, Catalog and Governance Backend | backend | critical | 450 |
| task_03 | Operational, Customer and Capture Backend | backend | critical | 275 |
| task_04 | Canonical GraphQL, Typed Operations and UI Foundation | chore | critical | 25 |
| task_05 | Complete Evidence Trail Admin | frontend | high | 67 |
| task_06 | Complete Operational and Customer Dashboard | frontend | critical | 17 |
| task_07 | Complete Capture PWA and Recovery | frontend | high | 4 |
| task_08 | Executable Parity Gate and Legacy Retirement | infra | critical | 28 |

Total contract assignment: 462 unit, 363 integration, and 66 end-to-end cases; 891 cases overall.
