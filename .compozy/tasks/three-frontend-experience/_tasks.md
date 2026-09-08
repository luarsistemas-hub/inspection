---
schema_version: "compozy.tasks/v2"
workflow: three-frontend-experience
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
      to: task_04
    - from: task_01
      to: task_05
    - from: task_01
      to: task_06
    - from: task_02
      to: task_04
    - from: task_02
      to: task_05
    - from: task_03
      to: task_04
    - from: task_03
      to: task_05
    - from: task_03
      to: task_06
    - from: task_04
      to: task_07
    - from: task_05
      to: task_07
    - from: task_06
      to: task_07
---

# Three Frontend Experience Task List

Seven robust tasks deliver the backend access and customer-visibility contracts, a versioned design system, three standalone frontends, and the final runtime cutover.

| Task | Title | Type | Complexity | Assigned tests |
|---|---|---|---|---:|
| task_01 | Access, Identity and Authorization Foundation | backend | critical | 67 |
| task_02 | Report Publication, Customer Views and Notifications | backend | critical | 53 |
| task_03 | Versioned Shared Design System | frontend | medium | 4 |
| task_04 | Standalone Admin Frontend | frontend | high | 52 |
| task_05 | Standalone Role-Adaptive Dashboard | frontend | critical | 104 |
| task_06 | Standalone Capture PWA | frontend | high | 93 |
| task_07 | Three-Product Runtime, CI and Cutover | infra | critical | 8 |

Total contract assignment: 75 unit, 230 integration, and 76 end-to-end cases; 381 cases overall.
