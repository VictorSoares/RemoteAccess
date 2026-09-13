# REGRAS DO PROJETO REMOTEACCESS (INVIOLÁVEIS)

Este arquivo define regras de arquitetura e integridade que **NUNCA DEVEM SER MODIFICADAS OU REMOVIDAS** por nenhum assistente de IA.

---

## 1. Ícone Nativo e Recursos Windows (Windows 11 & 10)
- **ID do Recurso**: O recurso RT_GROUP_ICON no winres/winres.json **DEVE SER SEMPRE #1**. NUNCA renomeie para "APP" ou outro identificador textual, pois o Windows 11 ignora e quebra o ícone no Explorer e na barra de tarefas.
- **Camadas Multi-Resolução**: Devem existir sempre as 10 resoluções pré-renderizadas (icon_16.png, icon_20.png, icon_24.png, icon_32.png, icon_40.png, icon_48.png, icon_64.png, icon_96.png, icon_128.png, icon_256.png) no diretório winres/, geradas a partir do master winres/icon.png (9.042 bytes) via scripts/gen_icons.ps1.
- **Compilação de Recursos**: O script uild.ps1 deve sempre invocar go-winres gerando os arquivos .syso para md64 e 386.

---

## 2. Janela e Redimensionamento (Tamanho Mínimo)
- **Dimensões Mínimas**: A janela da aplicação no modo Edge App e no CSS possui limite rígido de **540x580 pixels** (min-width: 540px; min-height: 580px;).
- **Listener de Resize**: internal/server/web/app.js mantém o listener de esize ativo para impedir colapso de layout.
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

## 6. Documentação no Obsidian
- Toda e qualquer alteração de arquitetura ou comportamento deve ser registrada e mantida em sincronia no vault em D:\Obsidian\Victor-Vault\01-Projetos\RemoteAccess.md.
