# Architecture RFC 002: Voice-to-Full-Task Synthesis, Project Resolution & Backlog Disposition Control

## 1. Objective
Expand the StayPoint task creation system to intelligently structure voice-dictated and raw text tasks. This includes inferring the correct organization and project, dynamically sizing the generated engineering specification based on the input detail level, ensuring high-fidelity semantic titles, prompting for clarification on ambiguous tasks, and setting the execution disposition (e.g., active vs. backlog).

## 2. Multi-Step Project Candidate Resolution
Instead of relying on AI to magically guess an exact project name, the system queries the Paperclip API for a live list of `Project` candidates before inference.
1. The CLI queries `GET /api/companies/:companyId/projects` to retrieve live projects (using `company` flag or environment variables).
2. The candidates are injected directly into the LLM system prompt.
3. The LLM is forced to select a project strictly from the provided list, ensuring a valid reference for dispatch.

## 3. Live Paperclip Schema Injection
The LLM inference schema is tightly coupled to Paperclip's API schema requirements. We enforce strict JSON generation using structured inputs:
- Required parameters: `organization`, `project`, `title`, `description`, `priority`, `labels`, `assigneeRole`.
- Newly injected schema elements:
  - `status`: Maps semantic disposition to actual execution states.
  - `askClarification`: Allows multi-turn loops for ambiguous input.

## 4. Dynamic Task Sizing
The AI generator adjusts the depth of the output description based on the detail in the user's prompt:
- **Idea/Spike cards:** Brief notes or thoughts are assigned a semantic prefix (`Idea:`) and generate a concise description to capture the concept.
- **Comprehensive Specs:** Detailed technical dictation produces a full engineering spec, complete with Objectives, Scope, Boundaries, and Acceptance Criteria.

## 5. High-Fidelity Title Synthesis
Naive `"Implement "` prefixes are removed. We synthesize crisp, imperative titles limited to 72 characters with standard semantic prefixes (e.g., `Feature:`, `Fix:`, `Refactor:`, `Infra:`, `Idea:`).

## 6. Raw Voice Preservation
To preserve provenance and prevent hallucinations from destroying original intent, the raw user input is securely embedded within the generated markdown description under a `<details><summary>Original Voice Dictation</summary>` block.

## 7. Clarification Prompt Loops
If the user's request contains architectural forks or high ambiguity, the LLM sets the `askClarification` field in the `InferredTask`. Future work by the Presentation Specialist will render an interactive clarification modal allowing the user to provide more context before finalizing the dispatch.

## 8. Backlog vs. Active Execution Dispatch
The system applies Semantic Disposition Inference:
- If the prompt implies parking (e.g., "idea", "someday", "look into later"), the `status` is set to `backlog`.
- To preserve agent compute and tokens, backlog tasks are dispatched with an empty `assigneeAgentId` (`AssigneeAgentId: ""`), ensuring zero agent wakes.
- Active execution requests (e.g., "urgent", "start now") are dispatched with `status: "todo"` and assigned normally.

