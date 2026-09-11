# ⚡ RemoteAccess Portable

Software portátil de acesso remoto **bidirecional** (Casa ↔ Trabalho) de alta performance, sem necessidade de instalação, com suporte a **Acesso Não Supervisionado (ID e Senha Fixos + Inicialização com o Windows)**.

---

## 🎯 Principais Recursos

- **Zero Instalação:** Apenas um arquivo `RemoteAccess.exe` (~10 MB).
- **Sem Direitos de Administrador:** Não exige UAC ou instalação de drivers.
- **ID Fixo Permanente:** O computador mantém o mesmo ID para sempre (salvo em `config.json`).
- **Senha Fixa Personalizada:** Defina sua própria senha permanente para nunca precisar consultar o PC de novo.
- **⚡ Iniciar com o Windows (Auto-start):** Inicia automaticamente em segundo plano quando o Windows liga (sem abrir janelas desnecessárias).
- **Bidirecional:** Acesse o PC do trabalho a partir de casa ou o PC de casa a partir do trabalho.
- **Contorno de NAT e Firewall:** Conexões diretas via WebRTC e servidores STUN públicos (Google/Cloudflare).
- **Visualizador Fluido:** Visualização em tempo real com controle de mouse, scroll e atalhos (`Ctrl+Alt+Del`, `Win+D`, `Alt+Tab`).
- **Histórico Rápido:** Salva os últimos computadores acessados para reconectar com 1 clique.

---

## 🚀 Como Configurar para Acesso 24/7 (Estilo AnyDesk)

### 1. No computador que vai ficar ligado (ex: seu PC de Casa ou do Trabalho):
1. Execute `RemoteAccess.exe`.
2. No painel que abrir no navegador:
   - Clique em **"✏️ Alterar Senha Fixa"** e defina uma senha sua (ex: `MinhaSenha@2026`).
   - Marque a caixa: **"⚡ Iniciar automaticamente junto com o Windows"**.
   - Anote o seu **ID Fixo** (ex: `849 201`).
3. Pronto! Agora, mesmo se o computador reiniciar ou for ligado do zero, o RemoteAccess iniciará sozinho em segundo plano com a mesma senha e o mesmo ID.

### 2. Para conectar a partir de outro computador:
1. Abra `RemoteAccess.exe`.
2. Vá na aba **"🚀 Computador Remoto (Controlar)"**.
3. Digite o **ID** e a **sua Senha Fixa**.
4. Clique em **"🚀 Conectar e Assumir Controle"**.

---

## 🛠️ Como Compilar o Projeto

```powershell
# Compilar versão de produção otimizada (Release - ~10 MB)
powershell -ExecutionPolicy Bypass -File .\build.ps1 -Mode release
```
