---
schema_version: "compozy.tasks/v2"
workflow: checklist-laudo-descritivo
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
    - from: task_02
      to: task_03
    - from: task_03
      to: task_04
    - from: task_04
      to: task_05
    - from: task_02
      to: task_06
    - from: task_05
      to: task_07
    - from: task_06
      to: task_07
---

# Descriptive Checklist and Report Task List

Each node delivers a coherent vertical slice. An edge means the source task must finish before the destination task begins. The frontend scope/capture task can progress in parallel with analysis and report backend work after the capture contract is ready.

| Task | Type | Complexity | Scope | Assigned tests |
| --- | --- | --- | --- | --- |
| [task_01](task_01.md) | backend | high | Pin template scope and guard new move-out inspections | 23 UT, 15 IT, 0 E2E |
| [task_02](task_02.md) | backend | high | Record component coverage and handover data during capture | 15 UT, 15 IT, 0 E2E |
| [task_03](task_03.md) | backend | high | Produce descriptive and versioned analysis results | 25 UT, 7 IT, 0 E2E |
| [task_04](task_04.md) | backend | critical | Compose and publish immutable descriptive reports | 24 UT, 14 IT, 1 E2E |
| [task_05](task_05.md) | backend | high | Complete evidence-backed move-out comparison | 16 UT, 8 IT, 0 E2E |
| [task_06](task_06.md) | frontend | high | Build scope and capture interfaces | 8 UT, 0 IT, 3 E2E |
| [task_07](task_07.md) | frontend | high | Present descriptive and comparison reports across audiences | 8 UT, 1 IT, 6 E2E |
