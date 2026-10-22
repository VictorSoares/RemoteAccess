# RemoteAccess — contexto compartilhado de agentes

Preservar e seguir as regras específicas existentes em `GEMINI.md` (ícones, streaming, encerramento, mutex e documentação). Este arquivo complementa essas regras; não autoriza alteração de produção.


## Regra de ouro — arquivos e Git compartilhados (2026-10-21)

Antes de editar/gerar/substituir arquivos ou executar publicação autorizada, ler e aplicar a regra compartilhada em `C:/Users/Victor/.codex/skills/victor-shared-git-safety/SKILL.md`. Vale para Codex, Claude, Gemini e Paperclip. Conferir conteúdo existente, baseline local, branches/upstream e estado atual no servidor; preservar trabalho de Leandro/Victor/outros agentes, detectar mudanças entre leitura e escrita e repetir a conferência antes de publicar. Nunca sobrescrever trabalho concorrente, publicar snapshot antigo ou assumir que main/dev existem/estão sincronizadas. Sem autorização implícita de commit/push/merge/deploy ou criação remota de branch. Se a regra não puder ser lida, interromper a escrita e registrar a limitação; investigação somente leitura pode continuar.

## Branches verificadas em 21/10/2026

Consulta real `git ls-remote --heads origin` encontrou apenas `main`. Após fetch sem integração, HEAD local estava 2 commits à frente e 0 atrás de `origin/main`. Existiam modificações preexistentes em `build.ps1`, `cmd/remoteaccess/main.go`, `internal/server/web/app.js` e `web/app.js`; preservá-las. Revalidar essas observações em cada tarefa.

Fluxo proposto: branch de tarefa isolada -> revisão em `dev` -> validação -> promoção autorizada a `main`. `dev` ainda não foi criada e o vínculo de deploy precisa ser confirmado; não presumir que o fluxo já está ativo. Não criar/publicar branches nem mudar deploy/default branch sem autorização específica.
