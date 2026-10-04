# Handoff: executar o plano via Subagent-Driven Development

Você é o controlador. Esta conversa de brainstorm/spec/plano acabou. Não redesenhe e não implemente as tarefas você mesmo.

## Objetivo

Executar `docs/superpowers/plans/2026-10-04-cloudcall-desk-api.md` com o skill **subagent-driven-development**: um implementer novo por tarefa, review de spec+qualidade depois de cada uma, review amplo no fim.

## Arquivos

- Plano: `docs/superpowers/plans/2026-10-04-cloudcall-desk-api.md`
- Spec: `docs/superpowers/specs/2026-10-04-cloudcall-desk-api-design.md`
- Tutorial original: `NFON-Projeto-e-Tutorial.md` (contexto; o plano e a spec mandam)
- SDD: `~/.cursor/plugins/cache/cursor-public/superpowers/d884ae04edebef577e82ff7c4e143debd0bbec99/skills/subagent-driven-development/SKILL.md`
- Worktrees: `.../skills/using-git-worktrees/SKILL.md`
- Ledger: `.superpowers/sdd/progress.md` (ainda não existe)

## Primeiros passos

1. Ler o skill SDD por completo e segui-lo. Não resumir nem pular review.
2. Ler using-git-worktrees. O repo está em `main` (`origin/main`, ahead 1 com o commit da spec). Não implementar em `main`. Isolar (worktree + branch, por exemplo `feat/cloudcall-api`).
3. Conferir o ledger. Tarefas já marcadas complete não se reexecutam.
4. Pre-flight do plano. Se achar conflito com Global Constraints, perguntar ao usuário numa leva só. Se estiver limpo, seguir sem comentário.
5. O plano em `docs/superpowers/plans/` ainda está untracked. Inclua-o no branch da feature.
6. Executar as 16 tarefas em ordem, sem perguntar “continuo?” entre elas. Só parar se BLOCKED, ambiguidade real ou plano completo.
7. Ao terminar: review de branch inteiro e skill finishing-a-development-branch.

## Regras que o plano já fechou

- Explicações em português; código, identificadores e mensagens em inglês.
- Sem ORM, sem framework HTTP extra, sem imagem Docker da API, sem gerar TypeScript neste repo.
- TDD, `gofmt`, `go test`, `go vet`, commit por tarefa.
- Cliente nunca envia `organizationId`/`userId` como autorização.
- `DEMO_MODE` tem de ser `true`.
- Um implementer por vez. Modelo explícito em cada dispatch, conforme a seção Model Selection do SDD.
- Brief via `scripts/task-brief`; relatório em arquivo; review package via `scripts/review-package BASE HEAD` (BASE gravado antes do implementer, nunca `HEAD~1`).
