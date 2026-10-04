# Prompt para o agente novo

Copie o bloco abaixo e cole na primeira mensagem de um agente novo neste repositório.

---

Implemente a API CloudCall Desk neste repositório usando Subagent-Driven Development. Este chat não tem o histórico do brainstorm — leia os arquivos e execute.

## Skills (nessa ordem)

1. Leia e siga `superpowers:using-git-worktrees` se o skill exigir worktree isolada. Caso contrário, crie e use o branch `feat/cloudcall-api` a partir de `main`. Não implemente em `main`.
2. Leia e siga `superpowers:subagent-driven-development` do começo ao fim (implementer por tarefa, review de spec+qualidade, ledger, review final, `finishing-a-development-branch`).
3. Os implementers devem seguir `superpowers:test-driven-development`.
4. Quando precisar de API/versão oficial de Go, PostgreSQL ou pgx, use Context7.

Não use `executing-plans`. Não peça confirmação entre tarefas. Só pare se estiver BLOCKED de verdade, se o plano contradisser a spec de um jeito que você não possa resolver, ou quando as 16 tarefas + review final terminarem.

## Fonte da verdade

- Plano: `docs/superpowers/plans/2026-10-04-cloudcall-desk-api.md` (16 tarefas TDD). Execute na ordem. Não pule tarefas. Não antecipe tarefa futura.
- Spec: `docs/superpowers/specs/2026-10-04-cloudcall-desk-api-design.md`
- Tutorial original (contexto pedagógico, não é o plano): `NFON-Projeto-e-Tutorial.md`
- Brainstorm (já fechado): `docs/superpowers/brainstorm/2026-10-03-cloudcall-desk-api.md`

Se plano e spec divergirem, a spec governa o comportamento; o plano governa a ordem e os arquivos. Se um reviewer apontar conflito com texto obrigatório do plano, pergunte a mim qual manda — não “corrija” o plano sozinho.

## Estado atual (2026-10-04)

- Recorte: só backend neste repo (API Go, PostgreSQL, Docker Compose, OpenAPI, testes Go). Angular fica em outro repositório.
- Desenho aprovado e commitado. Plano escrito. Nenhuma tarefa de implementação começou.
- Ainda não há `go.mod`, `cmd/api` nem Compose. Comece na Task 1.
- Ledger: se existir `.superpowers/sdd/progress.md`, tarefas marcadas complete estão feitas — não as refaça. Se não existir, comece do zero.
- `docs/progress.md` do plano é o diário do tutorial; o ledger SDD é o mapa de retomada.

## Restrições que não pode relaxar

- Explicações em português; código, identificadores e mensagens da aplicação em inglês.
- Sem ORM, sem framework HTTP extra, sem Redis, sem broker, sem imagem Docker da API, sem gerar TypeScript aqui.
- Cliente não envia `organizationId`/`userId` como autorização. SQL filtra pela org demo do seed.
- `DEMO_MODE` tem de ser `true` ou a subida falha.
- Envelope de erro `{ code, message, fieldErrors?, requestId }`.
- CORS só para `CORS_ORIGIN` (padrão `http://localhost:4200`), inclusive SSE. Sem cookies. Sem proxy.
- Uma instância; hub SSE em memória; publicar evento só depois do commit.
- `testing` da stdlib. Integração com Postgres real; skip se `DATABASE_URL` estiver vazio.
- Versões: Go 1.25+ suportado, PostgreSQL 18, pgx v5 via `database/sql` (`github.com/jackc/pgx/v5/stdlib`). Confirme na doc oficial na Task 1/4.

## Como trabalhar

- Antes da Task 1: ler o plano e a spec, criar todos, conferir o ledger, fazer o scan de conflitos do SDD.
- Por tarefa: `scripts/task-brief` do skill → implementer → `scripts/review-package` → reviewer → consertar Critical/Important → só então marcar complete no ledger.
- Commits frequentes, como o plano pede. Não faça push a menos que eu peça.
- Ao terminar as 16 tarefas: review de branch inteira e o skill `finishing-a-development-branch` (não faça merge/PR sem eu escolher).

Comece agora pela Task 1.
