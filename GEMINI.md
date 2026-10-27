# REGRAS DO PROJETO REMOTEACCESS (INVIOLÁVEIS)

Este arquivo define regras de arquitetura e integridade que **NUNCA DEVEM SER MODIFICADAS OU REMOVIDAS** por nenhum assistente de IA.

---

## 1. Ícone Nativo e Recursos Windows (Windows 11 & 10)
- **ID do Recurso**: O recurso `RT_GROUP_ICON` no `winres/winres.json` **DEVE SER SEMPRE `#1`**. NUNCA renomeie para `"APP"` ou outro identificador textual, pois o Windows 11 ignora e quebra o ícone no Explorer e na barra de tarefas.
- **Ordem Decrescente Obrigatória (256px ➔ 16px)**: O primeiro item da lista `0000` deve ser **SEMPRE o master `icon.png` (256x256, 9.042 bytes)**, seguido em ordem decrescente por `icon_128.png`, `icon_96.png`, `icon_64.png`, `icon_48.png`, `icon_40.png`, `icon_32.png`, `icon_24.png`, `icon_20.png`, `icon_16.png`. Se a lista começar por 16px, as chamadas do Windows `ExtractIcon(..., 0)` extraem a miniatura e a esticam na barra de tarefas, deixando o ícone pixelado e borrado no Windows 11.
- **Bandeja e Classes de Janela**: `internal/tray/tray_windows.go` deve usar `ExtractIconExW` para obter `hIconSmall` (bandeja) e `hIconLarge` (janela), garantindo nitidez cristalina em qualquer DPI.
- **Compilação de Recursos**: O script `build.ps1` deve sempre invocar `go-winres` gerando os arquivos `.syso` para `amd64` e `386`.

---

## 2. Janela e Redimensionamento (Tamanho Mínimo)
- **Dimensões Mínimas**: A janela da aplicação no modo Edge App e no CSS possui limite rígido de **540x580 pixels** (min-width: 540px; min-height: 580px;).
- **Listener de Resize**: internal/server/web/app.js mantém o listener de esize ativo para impedir colapso de layout.
- **Container**: .container no style.css deve ter min-width: 500px; e overflow-x: auto;.

---

## 3. Streaming de Vídeo & Prevenção de Congelamento
- **Backpressure Guard**: Em internal/webrtc/host.go, o loop de streaming **DEVE** verificar Chan.BufferedAmount() > 256*1024 e descartar frames excedentes (frame-dropping seletivo). NUNCA envie frames sem checar backpressure, pois isso trava o canal WebRTC.
- **Render Queue Assíncrona**: Em internal/server/web/app.js, enderRawBlob utiliza controle de concorrência (isRenderingFrame + pendingBlob) e URL.revokeObjectURL para evitar vazamentos e corrida de quadros no canvas.

---

## 4. Sincronização Bidirecional de Desconexão
- **Controlador ➔ Host**: Ao fechar o visualizador (closeViewer), enviar comando close via DataChannel e WebSocket de sinalização antes de fechar conexões.
- **Host ➔ Controlador**: Ao clicar em encerrar acesso (kickActiveSession), enviar ActionClose e sinal de controle antes de finalizar.
- **Watchdog & Listeners**: O Host deve escutar OnClose nos DataChannels e mudanças de estado de conexão (Closed, Failed, Disconnected) para chamar h.Close() e liberar a interface na hora.

---

## 5. Bandeja do Sistema (Tray) & Elevação UAC
- **System Tray**: O módulo internal/tray/tray_windows.go deve permanecer ativo, permitindo restaurar a janela e acessar opções via clique ou botão direito.
- **Transição de Mutex**: A função cquireSingleInstanceLock deve suportar retry com espera estendida (3s) durante elevação de privilégios para não colidir com a instância anterior.

---

## 6. Captura de Tela Sem Tela Preta (Desktop Synchronization & CAPTUREBLT)
- **Sincronização de Desktop**: `internal/capture/screen_windows.go` DEVE invocar `attachThreadToInputDesktop()` (via `OpenInputDesktop` e `SetThreadDesktop`) antes de qualquer chamada GDI (`BitBlt` / `GetDC`), garantindo que o thread do Go acompanhe o Input Desktop interativo ativo do Windows. Sem essa associação explícita, o Windows rejeita o `BitBlt` silenciosamente (código 0) caso o processo rode em segundo plano ou haja troca de desktop, gerando tela 100% preta no visualizador.
- **Inclusão de DWM e Camadas**: Chamadas de captura GDI diretas devem conter `0x00CC0020 | 0x40000000` (`SRCCOPY | CAPTUREBLT`) para capturar janelas translúcidas e renderização composta do DWM (Windows 11/10).

---

## 7. Permanência dos Monitores Remotos (`[ 🖥️ 1 ] [ 🖥️ 2 ] [ 🌐 Ambas ]`)
- **Seletores Sempre Visíveis**: Os seletores de tela no visualizador web (`#web-viewer-mon-pills`) e desktop **NUNCA DEVEM SER OCULTADOS** com `display: none`.
- **Modo Ambas**: `updateWebViewerMonitors` deve sempre renderizar no mínimo `[ 🖥️ 1 ] [ 🖥️ 2 ] [ 🌐 Ambas ]` (`Math.max(2, total)`), permitindo alternar de monitor ou unificar todas as telas em um único canvas panorâmico via atalho `Ctrl+Alt+M` ou clique direto.

---

## 8. Dimensões da Dock Toolbar e Proteção contra Clipping
- **Largura Contida**: A barra dock do viewer web deve ter largura contida em torno de ~890px (`max-width: calc(100vw - 24px); width: max-content; padding: 4px 8px; gap: 4px;`).
- **Ações Secundárias em Ícones**: Ferramentas secundárias devem usar ícones compactos (`🕶️ Cortina`, `🔓 Auto-Lock`, `🔊 Áudio`, `📁 Arquivo`, `⛶ Tela Cheia`).
- **Proteção das Extremidades**: O alinhamento centralizado (`translateX(-50%)`) nunca pode empurrar o Host ID (esquerda) nem o botão `[ ✕ Sair ]` (direita) para fora das bordas da janela em nenhuma resolução (1080p escalada, 1366x768 ou 1280x720).
- **Breakpoint Responsivo**: `@media (max-width: 900px)` oculta labels mantendo os ícones para evitar quebras em telas reduzidas.

---

## 9. Teclado Físico, Tecla Windows & Dead Keys
- **Estado de Modificadores**: O módulo `internal/input/input_windows.go` deve rastrear o estado atômico de modificadores (`Ctrl`, `Alt`, `Shift`, `AltGr`) e invocar `ReleaseAllModifierKeys()` ao término da sessão.
- **Tecla Windows Física**: Tecla física Windows (`MetaLeft` / `MetaRight`) deve ser injetada como `VK_LWIN` / `VK_RWIN`, protegida por `navigator.keyboard.lock` para não acionar o menu Iniciar do computador do operador.
- **Acentuação**: Acentuação e composição por dead keys devem ser preservadas.

---

## 10. Documentação no Obsidian
- Toda e qualquer alteração de arquitetura ou comportamento deve ser registrada e mantida em sincronia no vault em D:\Obsidian\Victor-Vault\01-Projetos\RemoteAccess.md e notas relacionadas em `01-Projetos/RemoteAccess/`.

## Regra de ouro — arquivos e Git compartilhados (2026-10-21)

Antes de editar/gerar/substituir arquivos ou executar publicação autorizada, ler e aplicar a regra compartilhada em `C:/Users/Victor/.codex/skills/victor-shared-git-safety/SKILL.md`. Vale para Codex, Claude, Gemini e Paperclip. Conferir conteúdo existente, baseline local, branches/upstream e estado atual no servidor; preservar trabalho de Leandro/Victor/outros agentes, detectar mudanças entre leitura e escrita e repetir a conferência antes de publicar. Nunca sobrescrever trabalho concorrente, publicar snapshot antigo ou assumir que main/dev existem/estão sincronizadas. Sem autorização implícita de commit/push/merge/deploy ou criação remota de branch. Se a regra não puder ser lida, interromper a escrita e registrar a limitação; investigação somente leitura pode continuar.
