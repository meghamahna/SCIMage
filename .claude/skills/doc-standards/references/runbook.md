# Runbook template (docs/runbooks/<alert-or-task>.md)

```markdown
# 🚨 <Alert name / operational task>

**Severity:** <P1–P4>  **Owner:** <team>  **Last verified:** YYYY-MM-DD

<!-- toc -->
## 📑 Contents

- [Symptoms](#-symptoms)
- [Impact](#-impact)
- [Diagnose](#-diagnose)
- [Mitigate](#-mitigate)
- [Resolve / root cause](#-resolve--root-cause)
- [Rollback](#-rollback)
- [Follow-up](#-follow-up)

<!-- /toc -->

## 🔍 Symptoms
<What the alert or user report looks like. Dashboard links.>

## 🚦 Impact
<Who/what is affected and how badly.>

## 🔎 Diagnose
1. <Check>. Expected: <result>. If not, go to step N.
2. …

## 🔧 Mitigate
1. <Safe, reversible action first>. Expected: <result>.
2. <Escalation path if not resolved within X minutes.>

## 🐛 Resolve / root cause
<Common causes, with pointers to code/config path:line.>

## 🔄 Rollback
<Exact steps to undo the mitigation.>

## 📋 Follow-up
- Postmortem link, tickets.
```

Rules:
- Each step is one action plus its expected result.
- Commands must be copy-pasteable, and secrets come from the env or secret manager by **name** only.
- Destructive steps are clearly marked, and each has a confirmation step.
