---
schema_version: "compozy.tasks/v2"
workflow: autonomous-inspection-platform
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
  edges:
    - from: task_01
      to: task_02
    - from: task_01
      to: task_03
    - from: task_02
      to: task_04
    - from: task_03
      to: task_04
    - from: task_04
      to: task_05
    - from: task_05
      to: task_06
    - from: task_06
      to: task_07
---

# Autonomous Inspection Platform Task List

Seven robust implementation tasks deliver the platform in dependency waves. Tasks 02 and 03 are the only parallel wave; later tasks consume their established runtime and domain contracts.

| Task | Title | Type | Complexity | Assigned tests |
|---|---|---|---|---:|
| task_01 | Foundation, Tenant Isolation, Access & Audit | infra | critical | 101 |
| task_02 | Async Messaging, External Sessions & Storage | infra | critical | 57 |
| task_03 | Declarative Catalog, Participants & Assets | backend | high | 81 |
| task_04 | Scheduling, Inspection & Project Lifecycle | backend | high | 111 |
| task_05 | Origin, Capture, Media & Directed Recapture | backend | critical | 165 |
| task_06 | Analysis, Reports, Dashboard & Retention | backend | critical | 148 |
| task_07 | Unified Next.js Experience & Acceptance | frontend | critical | 44 |

Total contract assignment: 73 unit, 600 integration, and 34 end-to-end cases; 707 cases overall.
