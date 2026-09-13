// RemoteAccess AnyDesk Portable Edition - Client & Host Engine

let myHostInfo = {
  id: '',
  alias: '',
  pwd: '',
  rawPwd: '',
  autoStart: false,
  signalingURL: '',
  cloudStatus: 'local'
};

let signalingWS = null;
let peerConnection = null;
let inputChannel = null;
let videoChannel = null;
let isConnected = false;
let currentTab = 'host';
let pwdVisible = false;
let currentClientSessionId = 'c_' + Math.random().toString(36).substring(2, 9);
let currentTargetId = '';
let logsModalOpen = false;
let pingIntervalTimer = null;
let sessionPollingTimer = null;
let isMouseDown = false;

// Performance counters
let frameCount = 0;
let lastFpsTime = performance.now();
let lastPingTime = 0;

const canvas = document.getElementById('screen-canvas');
const ctx = canvas.getContext('2d');
const viewerContainer = document.getElementById('viewer-container');

// Dialog helper for Custom In-App Glassmorphic Modals
let dialogResolve = null;

function showModalAlert(title, message, icon = 'ℹ️') {
  return new Promise((resolve) => {
    dialogResolve = resolve;
    document.getElementById('dialog-icon').innerText = icon;
    document.getElementById('dialog-title').innerText = title;
    document.getElementById('dialog-message').innerText = message;
    document.getElementById('dialog-input').style.display = 'none';
    document.getElementById('dialog-btn-cancel').style.display = 'none';
    document.getElementById('dialog-btn-ok').innerText = 'OK';
    document.getElementById('app-dialog-modal').style.display = 'flex';
  });
}

function showModalConfirm(title, message, icon = '⚠️', okText = 'Confirmar', cancelText = 'Cancelar') {
  return new Promise((resolve) => {
    dialogResolve = resolve;
    document.getElementById('dialog-icon').innerText = icon;
    document.getElementById('dialog-title').innerText = title;
    document.getElementById('dialog-message').innerText = message;
    document.getElementById('dialog-input').style.display = 'none';
    document.getElementById('dialog-btn-cancel').innerText = cancelText;
    document.getElementById('dialog-btn-cancel').style.display = 'inline-block';
    document.getElementById('dialog-btn-ok').innerText = okText;
    document.getElementById('app-dialog-modal').style.display = 'flex';
  });
}

function showModalPrompt(title, message, defaultValue = '', icon = '✏️', okText = 'Salvar', cancelText = 'Cancelar') {
  return new Promise((resolve) => {
    dialogResolve = resolve;
    document.getElementById('dialog-icon').innerText = icon;
    document.getElementById('dialog-title').innerText = title;
    document.getElementById('dialog-message').innerText = message;
    const input = document.getElementById('dialog-input');
    input.style.display = 'block';
    input.value = defaultValue;
    document.getElementById('dialog-btn-cancel').innerText = cancelText;
    document.getElementById('dialog-btn-cancel').style.display = 'inline-block';
    document.getElementById('dialog-btn-ok').innerText = okText;
    document.getElementById('app-dialog-modal').style.display = 'flex';
    setTimeout(() => {
      input.focus();
      input.select();
    }, 60);
  });
}

function closeAppDialog(result) {
  const modal = document.getElementById('app-dialog-modal');
  modal.style.display = 'none';
  const input = document.getElementById('dialog-input');
  const isPrompt = input.style.display !== 'none';
  if (dialogResolve) {
    if (isPrompt) {
      dialogResolve(result ? input.value : null);
    } else {
      dialogResolve(result);
    }
    dialogResolve = null;
  }
}

// Initialize application
window.addEventListener('DOMContentLoaded', async () => {
  const dlgInput = document.getElementById('dialog-input');
  if (dlgInput) {
    dlgInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        closeAppDialog(true);
      } else if (e.key === 'Escape') {
        e.preventDefault();
        closeAppDialog(false);
      }
    });
  }

  // Auto-hide top toolbar in viewer mode with top-edge detection
  window.addEventListener('mousemove', (e) => {
    if (!isConnected) return;
    const dock = document.getElementById('viewer-toolbar');
    if (!dock) return;
    if (e.clientY <= 55) {
      dock.classList.add('dock-visible');
    } else if (e.clientY > 85 && !dock.matches(':hover') && !dock.matches(':focus-within')) {
      dock.classList.remove('dock-visible');
    }
  });

  const tabHandle = document.getElementById('viewer-tab-handle');
  if (tabHandle) {
    tabHandle.addEventListener('mouseenter', () => {
      const dock = document.getElementById('viewer-toolbar');
      if (dock) dock.classList.add('dock-visible');
    });
  }

  // Smooth visibility recovery when window is minimized and restored
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') {
      isRenderingFrame = false;
      if (pendingBlob) {
        const next = pendingBlob;
        pendingBlob = null;
        renderRawBlob(next);
      }
      if (isConnected) {
        sendControl({ t: 'ping', ts: performance.now() });
      }
    }
  });

  loadRecentConnections();
  await fetchHostInfo();
  await fetchSystemInfo();
  await refreshLogs();
  connectSignaling();
  setupCanvasEvents();
  setupKeyboardEvents();

  sessionPollingTimer = setInterval(() => {
    fetchHostInfo();
    fetchSessionStatus();
    refreshLogs();
  }, 1000);
});

function switchTab(tab) {
  currentTab = tab;
  document.getElementById('tab-host-btn').classList.toggle('active', tab === 'host');
  document.getElementById('tab-client-btn').classList.toggle('active', tab === 'client');
  document.getElementById('host-view').style.display = tab === 'host' ? 'block' : 'none';
  document.getElementById('client-view').style.display = tab === 'client' ? 'block' : 'none';
}

let isInputBlocked = false;

async function fetchHostInfo() {
  try {
    const res = await fetch('/api/host-info');
    const data = await res.json();
    myHostInfo.id = data.id;
    myHostInfo.alias = data.alias || 'Meu PC';
    myHostInfo.rawPwd = data.password;
    myHostInfo.autoStart = data.auto_start;
    myHostInfo.signalingURL = data.signaling_url || '';
    myHostInfo.cloudStatus = data.cloud_status || 'local';
    
    document.getElementById('my-id').innerText = formatID(data.id);
    const aliasEl = document.getElementById('my-alias');
    if (aliasEl) aliasEl.innerText = myHostInfo.alias;
    
    const adminBadge = document.getElementById('admin-privilege-badge');
    const btnElevateLabel = document.getElementById('btn-elevate-label');
    if (data.is_admin) {
      if (adminBadge) {
        adminBadge.className = 'badge-fixed badge-admin';
        adminBadge.innerText = '🛡️ Administrador (Acesso Total UAC)';
      }
      if (btnElevateLabel) btnElevateLabel.innerText = 'Admin Ativo';
    } else {
      if (adminBadge) {
        adminBadge.className = 'badge-fixed badge-user';
        adminBadge.innerText = '🛡️ Usuário Padrão (Portátil)';
      }
      if (btnElevateLabel) btnElevateLabel.innerText = 'Modo Admin';
    }

    document.getElementById('autostart-toggle').checked = !!data.auto_start;
    const saveLogEl = document.getElementById('savelog-toggle');
    if (saveLogEl) saveLogEl.checked = !!data.save_log_file;
    updatePasswordDisplay();
    updateNetworkBadge(data.cloud_status, data.signaling_url);
  } catch (err) {
    console.error('Failed to fetch host info:', err);
  }
}

async function handleElevateOrInstall() {
  const confirmed = await showModalConfirm(
    'Executar como Administrador',
    '🛡️ Deseja reiniciar o RemoteAccess com privilégios de Administrador?\n\n' +
    'Isso permite que o operador remoto consiga controlar janelas elevadas do Windows (UAC, Gerenciador de Tarefas, Regedit e instaladores) sem bloqueio do mouse/teclado.',
    '🛡️',
    'Elevar para Admin',
    'Cancelar'
  );
  if (confirmed) {
    try {
      const res = await fetch('/api/elevate', { method: 'POST' });
      const data = await res.json();
      if (data.status === 'ok') {
        await showModalAlert('Elevação Solicitada', 'Confirme a janela do Controle de Conta de Usuário (UAC) na tela para reiniciar como Administrador.', '🚀');
      }
    } catch (e) {
      await showModalAlert('Erro', 'Falha ao solicitar elevação de privilégios.', '❌');
    }
  }
}

async function openCustomAliasModal() {
  const current = myHostInfo.alias || '';
  const newAlias = await showModalPrompt('Alterar Apelido', 'Digite o novo apelido/nome de identificação para este computador:', current, '🏷️');
  if (!newAlias || newAlias.trim() === '') return;

  try {
    const res = await fetch('/api/set-alias', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ alias: newAlias.trim() })
    });
    const data = await res.json();
    if (data.status === 'ok') {
      myHostInfo.alias = data.alias;
      const aliasEl = document.getElementById('my-alias');
      if (aliasEl) aliasEl.innerText = data.alias;
      await showModalAlert('Apelido Atualizado', 'Apelido do computador atualizado com sucesso!', '✅');
    }
  } catch (err) {
    await showModalAlert('Erro', 'Erro ao salvar novo apelido.', '❌');
  }
}

async function toggleSaveLogFile(e) {
  const enable = e.target.checked;
  try {
    const res = await fetch('/api/set-log-file', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enable: enable })
    });
    const data = await res.json();
    if (data.status === 'ok') {
      if (enable) {
        await showModalAlert('Logs em Disco', 'Gravação de arquivo de log em disco ativada (%APPDATA%\\RemoteAccess\\remoteaccess.log).', '📜');
      } else {
        await showModalAlert('Logs em Disco', 'Gravação de arquivo de log desativada. A pasta permanecerá limpa.', '🧹');
      }
    }
  } catch (err) {
    await showModalAlert('Erro', 'Erro ao salvar configuração de log.', '❌');
    e.target.checked = !enable;
  }
}

function sendSystemAction(action) {
  if (!isConnected) return;
  if (action === 'cad') {
    sendControl({ t: 'kd', k: 'Control', c: 'ControlLeft', kc: 17 });
    sendControl({ t: 'kd', k: 'Alt', c: 'AltLeft', kc: 18 });
    sendControl({ t: 'kd', k: 'Delete', c: 'Delete', kc: 46 });
    setTimeout(() => {
      sendControl({ t: 'ku', k: 'Delete', c: 'Delete', kc: 46 });
      sendControl({ t: 'ku', k: 'Alt', c: 'AltLeft', kc: 18 });
      sendControl({ t: 'ku', k: 'Control', c: 'ControlLeft', kc: 17 });
    }, 120);
  } else if (action === 'win_r') {
    sendControl({ t: 'sys_cmd', cmd: 'win_r' });
  } else if (action === 'alt_tab') {
    sendControl({ t: 'sys_cmd', cmd: 'alt_tab' });
  } else {
    sendControl({ t: 'sys_cmd', cmd: action });
  }
}

function toggleBlockInput() {
  if (!isConnected) return;
  isInputBlocked = !isInputBlocked;
  sendControl({ t: 'sys_cmd', cmd: 'block_input', block: isInputBlocked });
  const btn = document.getElementById('btn-block-input');
  if (btn) {
    btn.classList.toggle('btn-active', isInputBlocked);
    btn.innerText = isInputBlocked ? '🔒 Entrada Bloqueada' : '🚫 Bloquear Entrada';
  }
}

function getCanvasCoords(e) {
  const rect = canvas.getBoundingClientRect();
  const nativeWidth = canvas.width || 1920;
  const nativeHeight = canvas.height || 1080;
  
  // Calculate aspect ratios
  const containerRatio = rect.width / rect.height;
  const imageRatio = nativeWidth / nativeHeight;

  let actualWidth, actualHeight, offsetX, offsetY;

  if (containerRatio > imageRatio) {
    // Letterbox on sides (pillarbox)
    actualHeight = rect.height;
    actualWidth = rect.height * imageRatio;
    offsetX = (rect.width - actualWidth) / 2;
    offsetY = 0;
  } else {
    // Letterbox on top & bottom
    actualWidth = rect.width;
    actualHeight = rect.width / imageRatio;
    offsetX = 0;
    offsetY = (rect.height - actualHeight) / 2;
  }

  const mouseX = e.clientX - rect.left - offsetX;
  const mouseY = e.clientY - rect.top - offsetY;

  const ratioX = mouseX / actualWidth;
  const ratioY = mouseY / actualHeight;

  return {
    x: Math.max(0, Math.min(1, ratioX)),
    y: Math.max(0, Math.min(1, ratioY))
  };
}

async function fetchSystemInfo() {
  try {
    const res = await fetch('/api/system-info');
    const data = await res.json();
    if (data.hostname) document.getElementById('sys-hostname').innerText = data.hostname;
    if (data.os) document.getElementById('sys-os').innerText = data.os;
    if (data.mac) {
      const macEl = document.getElementById('sys-mac');
      if (macEl) macEl.innerText = data.mac;
    }
    if (data.monitors) {
      document.getElementById('sys-monitors').innerText = `${data.monitors} Monitor(es)`;
    }
  } catch (err) {}
}

function updateViewerMonitors(count) {
  const monSelect = document.getElementById('viewer-monitor-select');
  if (!monSelect) return;
  const currentVal = monSelect.value;
  monSelect.innerHTML = '';
  const total = Math.max(1, count || 1);
  for (let i = 0; i < total; i++) {
    const opt = document.createElement('option');
    opt.value = i;
    opt.innerText = `🖥️ Monitor ${i + 1}`;
    monSelect.appendChild(opt);
  }
  if (currentVal && parseInt(currentVal) < total) {
    monSelect.value = currentVal;
  } else {
    monSelect.value = 0;
  }
}

let hostChatOpen = false;
let lastSeenHostMsgCount = 0;

async function fetchSessionStatus() {
  try {
    const res = await fetch('/api/session-status');
    const data = await res.json();
    const box = document.getElementById('active-session-box');
    if (data.active) {
      box.style.display = 'flex';
      const clientLabel = data.client_alias ? `${data.client_alias} (${formatID(data.client_id)})` : (data.client_id || 'Controlador');
      document.getElementById('host-session-client-id').innerText = clientLabel;
      const m = Math.floor(data.duration / 60).toString().padStart(2, '0');
      const s = (data.duration % 60).toString().padStart(2, '0');
      document.getElementById('host-session-duration').innerText = `${m}:${s}`;
      await refreshHostChat();
    } else {
      if (box) box.style.display = 'none';
      const chatSec = document.getElementById('host-chat-container');
      if (chatSec) chatSec.style.display = 'none';
      const floatingDrawer = document.getElementById('host-floating-chat-drawer');
      if (floatingDrawer) floatingDrawer.style.display = 'none';
      const badge = document.getElementById('host-chat-badge');
      if (badge) {
        badge.innerText = '0';
        badge.style.display = 'none';
      }
      const chatMessages = document.getElementById('host-chat-messages');
      if (chatMessages && lastSeenHostMsgCount > 0) {
        chatMessages.innerHTML = '<div class="chat-intro">💬 Bate-papo em tempo real com o operador remoto.</div>';
      }
      const floatMessages = document.getElementById('host-floating-chat-messages');
      if (floatMessages && lastSeenHostMsgCount > 0) {
        floatMessages.innerHTML = '<div class="chat-intro">Sessão de chat conectada com o operador remoto</div>';
      }
      hostChatOpen = false;
      lastSeenHostMsgCount = 0;
      hideHostToast();
    }
  } catch (err) {}
}

let appAudioCtx = null;
function getAppAudioContext() {
  if (!appAudioCtx) {
    const AudioCtx = window.AudioContext || window.webkitAudioContext;
    if (AudioCtx) {
      appAudioCtx = new AudioCtx();
    }
  }
  if (appAudioCtx && appAudioCtx.state === 'suspended') {
    appAudioCtx.resume().catch(() => {});
  }
  return appAudioCtx;
}

window.addEventListener('pointerdown', () => { getAppAudioContext(); }, { once: true });
window.addEventListener('keydown', () => { getAppAudioContext(); }, { once: true });

function playChatChime() {
  try {
    const ctx = getAppAudioContext();
    if (!ctx) return;
    const now = ctx.currentTime;
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();

    osc.type = 'sine';
    osc.frequency.setValueAtTime(587.33, now);
    osc.frequency.setValueAtTime(880, now + 0.09);

    gain.gain.setValueAtTime(0.35, now);
    gain.gain.exponentialRampToValueAtTime(0.001, now + 0.4);

    osc.connect(gain);
    gain.connect(ctx.destination);

    osc.start(now);
    osc.stop(now + 0.4);
  } catch(e) {}
}

let toastTimer = null;
function showChatToast(sender, text) {
  const isViewer = isConnected;
  const toastId = isViewer ? 'chat-toast-popup' : 'host-toast-popup';
  const toast = document.getElementById(toastId) || document.getElementById('chat-toast-popup');
  if (!toast) return;

  const titleEl = toast.querySelector('.chat-toast-title');
  const textEl = toast.querySelector('.chat-toast-text');
  if (titleEl) titleEl.innerText = `💬 Mensagem de ${sender}`;
  if (textEl) textEl.innerText = text;

  toast.style.display = 'flex';
  playChatChime();

  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    hideChatToast();
    hideHostToast();
  }, 8000);
}

function hideChatToast() {
  const toast = document.getElementById('chat-toast-popup');
  if (toast) toast.style.display = 'none';
}

function hideHostToast() {
  const toast = document.getElementById('host-toast-popup');
  if (toast) toast.style.display = 'none';
}

function handleToastClick() {
  hideChatToast();
  const drawer = document.getElementById('chat-drawer');
  if (drawer) {
    drawer.style.display = 'flex';
    const input = document.getElementById('chat-input');
    if (input) setTimeout(() => input.focus(), 60);
  }
}

function handleHostToastClick() {
  hideHostToast();
  toggleHostFloatingChat(true);
}

function toggleHostFloatingChat(forceOpen) {
  const drawer = document.getElementById('host-floating-chat-drawer');
  if (!drawer) return;
  if (forceOpen === true) {
    hostChatOpen = true;
  } else if (forceOpen === false) {
    hostChatOpen = false;
  } else {
    hostChatOpen = !hostChatOpen;
  }
  drawer.style.display = hostChatOpen ? 'flex' : 'none';
  if (hostChatOpen) {
    hideHostToast();
    const badge = document.getElementById('host-chat-badge');
    if (badge) {
      badge.innerText = '0';
      badge.style.display = 'none';
    }
    const container = document.getElementById('host-floating-chat-messages');
    if (container) container.scrollTop = container.scrollHeight;
    const input = document.getElementById('host-floating-chat-input');
    if (input) setTimeout(() => input.focus(), 60);
  }
}

async function refreshHostChat() {
  try {
    const res = await fetch('/api/chat-messages');
    const data = await res.json();
    if (!data.messages) return;

    const container = document.getElementById('host-chat-messages');
    const floatContainer = document.getElementById('host-floating-chat-messages');
    if (data.messages.length !== lastSeenHostMsgCount) {
      const prevCount = lastSeenHostMsgCount;
      lastSeenHostMsgCount = data.messages.length;

      const buildChatHTML = () => {
        let html = '';
        data.messages.forEach(msg => {
          const isMe = msg.sender.includes('Host') || msg.sender.includes('Você');
          html += `<div class="msg-bubble ${isMe ? 'msg-mine' : 'msg-other'}"><strong style="font-size: 0.72rem; opacity: 0.85;">${msg.sender}</strong><div>${msg.text}</div><div class="msg-meta">${msg.time}</div></div>`;
        });
        return html;
      };

      const chatHtml = buildChatHTML();
      if (container) {
        container.innerHTML = chatHtml;
        container.scrollTop = container.scrollHeight;
      }
      if (floatContainer) {
        floatContainer.innerHTML = chatHtml;
        floatContainer.scrollTop = floatContainer.scrollHeight;
      }

      const newSlice = data.messages.slice(prevCount);
      for (const msg of newSlice) {
        const isMe = msg.sender.includes('Host') || msg.sender.includes('Você');
        if (!isMe) {
          showChatToast(msg.sender, msg.text);
          // Auto-open floating chat window for host so customer sees it immediately on bottom-right!
          toggleHostFloatingChat(true);
          const badge = document.getElementById('host-chat-badge');
          if (badge && !hostChatOpen) {
            badge.innerText = parseInt(badge.innerText || '0') + 1;
            badge.style.display = 'inline-flex';
          }
          break;
        }
      }
    }
  } catch (err) {}
}

function toggleHostChat() {
  toggleHostFloatingChat();
  const chatSec = document.getElementById('host-chat-container');
  if (chatSec) chatSec.style.display = hostChatOpen ? 'flex' : 'none';
  if (hostChatOpen) {
    const badge = document.getElementById('host-chat-badge');
    if (badge) {
      badge.innerText = '0';
      badge.style.display = 'none';
    }
    const container = document.getElementById('host-chat-messages');
    if (container) container.scrollTop = container.scrollHeight;
    const input = document.getElementById('host-chat-input');
    if (input) setTimeout(() => input.focus(), 60);
  }
}

async function sendHostChatMessage(e) {
  if (e) e.preventDefault();
  const input = document.getElementById('host-chat-input');
  if (!input) return;
  const text = input.value.trim();
  if (!text) return;
  input.value = '';

  try {
    await fetch('/api/send-chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text: text })
    });
    await refreshHostChat();
  } catch (err) {}
}

async function sendHostFloatingChatMessage(e) {
  if (e) e.preventDefault();
  const input = document.getElementById('host-floating-chat-input');
  if (!input) return;
  const text = input.value.trim();
  if (!text) return;
  input.value = '';

  try {
    await fetch('/api/send-chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text: text })
    });
    await refreshHostChat();
  } catch (err) {}
}

async function kickActiveSession() {
  const confirmed = await showModalConfirm(
    'Encerrar Acesso Remoto',
    'Deseja realmente desconectar o operador remoto e encerrar a sessão imediatamente?',
    '🛑',
    'Encerrar Sessão',
    'Cancelar'
  );
  if (!confirmed) return;

  try {
    await fetch('/api/kick-session', { method: 'POST' });
    const box = document.getElementById('active-session-box');
    if (box) box.style.display = 'none';
    const chatSec = document.getElementById('host-chat-container');
    if (chatSec) chatSec.style.display = 'none';
    const floatingDrawer = document.getElementById('host-floating-chat-drawer');
    if (floatingDrawer) floatingDrawer.style.display = 'none';
    const badge = document.getElementById('host-chat-badge');
    if (badge) {
      badge.innerText = '0';
      badge.style.display = 'none';
    }
    const chatMessages = document.getElementById('host-chat-messages');
    if (chatMessages) {
      chatMessages.innerHTML = '<div class="chat-intro">💬 Bate-papo em tempo real com o operador remoto.</div>';
    }
    hostChatOpen = false;
    lastSeenHostMsgCount = 0;
    hideHostToast();
    await fetchSessionStatus();
    await showModalAlert('Sessão Encerrada', 'A sessão remota foi desconectada com sucesso.', '✅');
  } catch (err) {
    await showModalAlert('Erro', 'Falha ao encerrar a sessão remota.', '❌');
  }
}

function handlePowerDropdown(action) {
  const select = document.getElementById('viewer-power-select');
  if (select) select.value = '';
  if (!action) return;
  if (action === 'lock') {
    sendSystemAction('lock');
  } else if (action === 'reboot' || action === 'shutdown' || action === 'suspend') {
    sendPowerAction(action);
  }
}

async function sendPowerAction(action) {
  if (!isConnected) return;
  if (action === 'reboot') {
    const confirmed = await showModalConfirm('Reiniciar Computador Remoto', '⚠️ Deseja realmente REINICIAR o computador remoto?\n\nO sistema será reiniciado em 5 segundos e você poderá reconectar assim que ele inicializar.', '🔄', 'Reiniciar', 'Cancelar');
    if (confirmed) {
      sendControl({ t: 'sys_cmd', cmd: 'reboot' });
      await showModalAlert('Comando Enviado', 'Comando de reinicialização enviado ao computador remoto.', '🚀');
    }
  } else if (action === 'shutdown') {
    const confirmed = await showModalConfirm('Desligar Computador Remoto', '🛑 ATENÇÃO: Deseja realmente DESLIGAR o computador remoto?\n\nEle será desligado completamente. Para ligá-lo novamente à distância, será necessário utilizar Wake-on-LAN (WoL).', '🛑', 'Desligar Agora', 'Cancelar');
    if (confirmed) {
      sendControl({ t: 'sys_cmd', cmd: 'shutdown' });
      await showModalAlert('Comando Enviado', 'Comando de desligamento enviado ao computador remoto.', '🛑');
    }
  } else if (action === 'suspend') {
    const confirmed = await showModalConfirm('Suspender Computador Remoto', '🌙 Deseja colocar o computador remoto em modo de SUSPENSÃO (Sleep/Repouso)?', '🌙', 'Suspender', 'Cancelar');
    if (confirmed) {
      sendControl({ t: 'sys_cmd', cmd: 'suspend' });
    }
  }
}

function updateNetworkBadge(status, url) {
  const dot = document.getElementById('status-indicator');
  const text = document.getElementById('status-text');

  if (!url || status === 'local') {
    dot.className = 'status-dot online';
    text.innerText = 'Pronto (Rede Local)';
  } else if (status === 'connected') {
    dot.className = 'status-dot online';
    text.innerText = 'Online (Nuvem Ativa)';
  } else if (status === 'connecting') {
    dot.className = 'status-dot';
    text.innerText = 'Conectando à Nuvem...';
  } else {
    dot.className = 'status-dot';
    text.innerText = 'Desconectado da Nuvem';
  }
}

async function toggleLogsModal() {
  const modal = document.getElementById('logs-modal');
  logsModalOpen = !logsModalOpen;
  modal.style.display = logsModalOpen ? 'flex' : 'none';
  if (logsModalOpen) {
    await refreshLogs();
  }
}

async function refreshLogs() {
  try {
    const res = await fetch('/api/logs');
    const data = await res.json();
    const modalContainer = document.getElementById('logs-container');
    const hostTerminal = document.getElementById('host-terminal-logs');
    
    let content = 'Nenhum log registrado ainda.';
    if (data.logs && data.logs.length > 0) {
      content = data.logs.join('\n');
    }

    if (modalContainer && (logsModalOpen || modalContainer.innerText !== content)) {
      modalContainer.innerText = content;
      modalContainer.scrollTop = modalContainer.scrollHeight;
    }

    if (hostTerminal) {
      hostTerminal.innerText = content;
      hostTerminal.scrollTop = hostTerminal.scrollHeight;
    }
  } catch (err) {
    console.error('Failed to fetch logs:', err);
  }
}

function formatID(id) {
  if (!id) return '--- ---';
  const clean = id.replace(/\D/g, '');
  if (clean.length > 3) {
    return clean.slice(0, 3) + ' ' + clean.slice(3);
  }
  return clean;
}

function updatePasswordDisplay() {
  const pwdEl = document.getElementById('my-pwd');
  if (pwdVisible) {
    pwdEl.innerText = myHostInfo.rawPwd;
  } else {
    pwdEl.innerText = '••••••••';
  }
}

function togglePasswordVisibility() {
  pwdVisible = !pwdVisible;
  updatePasswordDisplay();
}

async function openCustomPasswordModal() {
  const newPwd = await showModalPrompt('Definir Senha Fixa', 'Digite a nova senha fixa permanente que deseja usar para este computador (mínimo 3 caracteres):', myHostInfo.rawPwd, '🔑');
  if (newPwd === null) return;
  if (!newPwd || newPwd.trim().length < 3) {
    await showModalAlert('Senha Inválida', 'A senha precisa ter pelo menos 3 caracteres.', '⚠️');
    return;
  }

  try {
    const res = await fetch('/api/set-password', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: newPwd.trim() })
    });
    const data = await res.json();
    if (data.status === 'ok') {
      myHostInfo.rawPwd = data.password;
      updatePasswordDisplay();
      await showModalAlert('Senha Fixa Salva', 'Senha fixa salva com sucesso! Ela nunca mais mudará.', '✅');
    }
  } catch (err) {
    await showModalAlert('Erro', 'Erro ao salvar nova senha.', '❌');
  }
}

async function toggleAutoStart(e) {
  const enable = e.target.checked;
  try {
    const res = await fetch('/api/autostart', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enable: enable })
    });
    const data = await res.json();
    if (data.status === 'ok') {
      myHostInfo.autoStart = enable;
      if (enable) {
        await showModalAlert('Inicialização com Windows', 'RemoteAccess agora iniciará automaticamente em segundo plano com o Windows!', '🚀');
      } else {
        await showModalAlert('Inicialização com Windows', 'Inicialização automática desativada.', 'ℹ️');
      }
    }
  } catch (err) {
    await showModalAlert('Erro', 'Erro ao atualizar inicialização automática.', '❌');
    e.target.checked = !enable;
  }
}

function copyToClipboard(elementId) {
  const el = document.getElementById(elementId);
  const text = elementId === 'my-pwd' ? myHostInfo.rawPwd : el.innerText.replace(/\s+/g, '');
  navigator.clipboard.writeText(text).then(async () => {
    await showModalAlert('Copiado', 'Copiado para a área de transferência: ' + text, '📋');
  });
}

function updateHostConfig() {
  const q = parseInt(document.getElementById('host-quality').value);
  const fps = parseInt(document.getElementById('host-fps').value);
  fetch('/api/config', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ quality: q, fps: fps })
  });
}

async function openSignalingModal() {
  const current = myHostInfo.signalingURL || '';
  const newUrl = await showModalPrompt('Configurar Servidor Nuvem', 'Digite o endereço do seu servidor Render (ex: https://remoteaccess-ltwx.onrender.com ou deixe em branco para modo local):', current, '🌐');
  if (newUrl === null) return;

  try {
    const res = await fetch('/api/set-signaling', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ signaling_url: newUrl.trim() })
    });
    const data = await res.json();
    if (data.status === 'ok') {
      myHostInfo.signalingURL = data.signaling_url;
      await showModalAlert('Servidor Configurado', 'Servidor de Nuvem configurado com sucesso!', '✅');
      await fetchHostInfo();
      connectSignaling();
    }
  } catch (err) {
    await showModalAlert('Erro', 'Erro ao salvar servidor de sinalização.', '❌');
  }
}

function connectSignaling() {
  if (signalingWS) {
    try { signalingWS.close(); } catch(e) {}
  }

  let wsUrl = '';
  if (myHostInfo.signalingURL && myHostInfo.signalingURL.trim() !== '') {
    wsUrl = myHostInfo.signalingURL.trim();
    if (wsUrl.startsWith('https://')) wsUrl = 'wss://' + wsUrl.slice(8);
    if (wsUrl.startsWith('http://')) wsUrl = 'ws://' + wsUrl.slice(7);
    if (!wsUrl.startsWith('ws://') && !wsUrl.startsWith('wss://')) wsUrl = 'wss://' + wsUrl;
    wsUrl = wsUrl.replace(/\/$/, '');
    if (!wsUrl.endsWith('/ws')) wsUrl += '/ws';
  } else {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    wsUrl = `${protocol}//${window.location.host}/ws`;
  }

  try {
    signalingWS = new WebSocket(wsUrl);
  } catch (err) {
    return;
  }

  signalingWS.onopen = () => {
    console.log('[Signaling WS] Conectado a:', wsUrl);
  };

  signalingWS.onmessage = async (event) => {
    try {
      const msg = JSON.parse(event.data);

      switch (msg.action) {
        case 'status':
          if (msg.status === 'auth_ok') {
            openViewer();
            startPingLoop();
          }
          break;

        case 'error':
          await showModalAlert('Erro na Conexão', msg.message || 'Falha ao conectar com o computador remoto.', '❌');
          resetConnectButton();
          break;

        case 'answer':
          if (peerConnection) {
            await peerConnection.setRemoteDescription(new RTCSessionDescription({
              type: 'answer',
              sdp: msg.sdp
            }));
          }
          break;

        case 'candidate':
          if (peerConnection && msg.candidate) {
            await peerConnection.addIceCandidate(new RTCIceCandidate(msg.candidate));
          }
          break;

        case 'data':
          if (typeof msg.payload === 'string' && msg.payload.startsWith('{')) {
            try {
              const ctrl = JSON.parse(msg.payload);
              if (ctrl.t === 'pong') {
                const rtt = Math.max(1, Math.round(performance.now() - ctrl.ts));
                const latEl = document.getElementById('stat-latency');
                if (latEl) latEl.innerText = `⚡ ${rtt} ms`;
                const badgeEl = document.getElementById('hud-status-badge');
                if (badgeEl) badgeEl.innerHTML = `<span class="hud-dot" style="background-color: #38bdf8; box-shadow: 0 0 6px #38bdf8;"></span> Nuvem Relay`;
              } else if (ctrl.t === 'chat') {
                appendChatMessage('Remoto', ctrl.text);
              } else if (ctrl.t === 'init_info' && ctrl.mon) {
                updateViewerMonitors(ctrl.mon);
              }
            } catch(e) {}
          } else {
            renderBase64Frame(msg.payload);
          }
          break;

        case 'close':
          const closeReason = msg.message || 'A sessão remota foi encerrada.';
          closeViewer();
          await showModalAlert('Sessão Encerrada', closeReason, 'ℹ️');
          break;
      }
    } catch (e) {
      console.error(e);
    }
  };

  signalingWS.onclose = () => {
    setTimeout(connectSignaling, 4000);
  };
}

// Client: Connect to Remote Host
async function connectToRemote(e) {
  if (e) e.preventDefault();
  const rawTargetId = document.getElementById('target-id').value.replace(/\D/g, '');
  const targetPwd = document.getElementById('target-pwd').value;

  if (!rawTargetId || !targetPwd) {
    await showModalAlert('Campos Obrigatórios', 'Por favor, informe o ID e a Senha do computador remoto.', '⚠️');
    return;
  }

  currentTargetId = rawTargetId;
  const btn = document.getElementById('btn-connect');
  btn.disabled = true;
  btn.innerHTML = '<span>⏳ Conectando...</span>';

  saveRecentConnection(rawTargetId, targetPwd);
  currentClientSessionId = 'c_' + Math.random().toString(36).substring(2, 9);

  if (!signalingWS || signalingWS.readyState !== WebSocket.OPEN) {
    connectSignaling();
    await new Promise(r => setTimeout(r, 800));
  }

  // 1. Send authentication request with alias and host ID
  if (signalingWS && signalingWS.readyState === WebSocket.OPEN) {
    signalingWS.send(JSON.stringify({
      action: 'connect',
      id: currentClientSessionId,
      targetId: rawTargetId,
      password: targetPwd,
      alias: (myHostInfo.alias || 'Meu PC') + ' (Controlador)',
      host_id: myHostInfo.id
    }));
  }

  // 2. Setup WebRTC Peer Connection
  const config = {
    iceServers: [
      { urls: 'stun:stun.l.google.com:19302' },
      { urls: 'stun:stun1.l.google.com:19302' },
      { urls: 'stun:stun2.l.google.com:19302' },
      { urls: 'stun:stun3.l.google.com:19302' },
      { urls: 'stun:stun4.l.google.com:19302' },
      { urls: 'stun:stun.cloudflare.com:3478' },
      { urls: 'stun:global.stun.twilio.com:3478' }
    ]
  };

  peerConnection = new RTCPeerConnection(config);
  inputChannel = peerConnection.createDataChannel('input', { ordered: true });
  videoChannel = peerConnection.createDataChannel('video', { maxRetransmits: 0, ordered: false });

  peerConnection.oniceconnectionstatechange = () => {
    console.log('[WebRTC] ICE Connection State:', peerConnection.iceConnectionState);
    if (peerConnection.iceConnectionState === 'connected') {
      document.getElementById('stat-latency').innerText = `⚡ P2P Ativo`;
    }
  };

  peerConnection.onconnectionstatechange = () => {
    console.log('[WebRTC] Connection State:', peerConnection.connectionState);
    if (peerConnection.connectionState === 'disconnected' || peerConnection.connectionState === 'failed' || peerConnection.connectionState === 'closed') {
      if (isConnected) {
        closeViewer();
        showModalAlert('Sessão Encerrada', 'A conexão com o computador remoto foi encerrada.', 'ℹ️');
      }
    }
  };

  setupDataChannels(rawTargetId);

  peerConnection.onicecandidate = (event) => {
    if (event.candidate && signalingWS && signalingWS.readyState === WebSocket.OPEN) {
      signalingWS.send(JSON.stringify({
        action: 'candidate',
        id: currentClientSessionId,
        targetId: rawTargetId,
        candidate: event.candidate.toJSON()
      }));
    }
  };

  try {
    const offer = await peerConnection.createOffer();
    await peerConnection.setLocalDescription(offer);

    if (signalingWS && signalingWS.readyState === WebSocket.OPEN) {
      signalingWS.send(JSON.stringify({
        action: 'offer',
        id: currentClientSessionId,
        targetId: rawTargetId,
        password: targetPwd,
        sdp: offer.sdp
      }));
    }
  } catch (err) {
    console.warn('WebRTC offer fallback to Relay:', err);
  }
}

function setupDataChannels(targetId) {
  inputChannel.onopen = () => {
    console.log('[Client] WebRTC P2P DataChannel Aberto!');
    openViewer();
    startPingLoop();
  };

  inputChannel.onclose = () => {
    console.log('[Client] Input DataChannel fechado.');
    if (isConnected) {
      closeViewer();
      showModalAlert('Sessão Encerrada', 'A sessão remota foi desconectada pelo Host.', 'ℹ️');
    }
  };

  inputChannel.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data);
      if (msg.t === 'pong') {
        const rtt = Math.max(1, Math.round(performance.now() - msg.ts));
        const latEl = document.getElementById('stat-latency');
        if (latEl) latEl.innerText = `⚡ ${rtt} ms`;
        const badgeEl = document.getElementById('hud-status-badge');
        if (badgeEl) badgeEl.innerHTML = `<span class="hud-dot"></span> P2P Direct`;
      } else if (msg.t === 'chat') {
        appendChatMessage('Remoto', msg.text);
      } else if (msg.t === 'init_info' && msg.mon) {
        updateViewerMonitors(msg.mon);
      } else if (msg.t === 'close' || (msg.t === 'sys_cmd' && msg.cmd === 'close')) {
        closeViewer();
        showModalAlert('Sessão Encerrada', msg.text || 'A sessão remota foi encerrada pelo computador remoto.', 'ℹ️');
      }
    } catch (e) {}
  };

  videoChannel.onmessage = async (event) => {
    renderRawBlob(event.data);
  };
}

let isRenderingFrame = false;
let pendingBlob = null;

function renderBase64Frame(b64) {
  if (!isConnected) openViewer();

  frameCount++;
  const now = performance.now();
  if (now - lastFpsTime >= 1000) {
    const fpsEl = document.getElementById('stat-fps');
    if (fpsEl) fpsEl.innerText = `🎥 ${frameCount} FPS`;
    frameCount = 0;
    lastFpsTime = now;
  }

  const img = new Image();
  img.onload = () => {
    if (canvas.width !== img.width || canvas.height !== img.height) {
      canvas.width = img.width;
      canvas.height = img.height;
    }
    ctx.drawImage(img, 0, 0);
  };
  img.src = 'data:image/jpeg;base64,' + b64;
}

let frameSafetyTimer = null;

function renderRawBlob(blobData) {
  frameCount++;
  const now = performance.now();
  if (now - lastFpsTime >= 1000) {
    const fpsEl = document.getElementById('stat-fps');
    if (fpsEl) fpsEl.innerText = `🎥 ${frameCount} FPS`;
    frameCount = 0;
    lastFpsTime = now;
  }

  if (isRenderingFrame) {
    pendingBlob = blobData;
    return;
  }

  isRenderingFrame = true;
  if (frameSafetyTimer) clearTimeout(frameSafetyTimer);
  frameSafetyTimer = setTimeout(() => {
    isRenderingFrame = false;
    if (pendingBlob) {
      const next = pendingBlob;
      pendingBlob = null;
      renderRawBlob(next);
    }
  }, 150);

  const blob = new Blob([blobData], { type: 'image/jpeg' });
  
  if (window.createImageBitmap) {
    createImageBitmap(blob).then((imgBitmap) => {
      if (frameSafetyTimer) clearTimeout(frameSafetyTimer);
      if (canvas.width !== imgBitmap.width || canvas.height !== imgBitmap.height) {
        canvas.width = imgBitmap.width;
        canvas.height = imgBitmap.height;
      }
      ctx.drawImage(imgBitmap, 0, 0);
      imgBitmap.close();
      isRenderingFrame = false;
      if (pendingBlob) {
        const next = pendingBlob;
        pendingBlob = null;
        renderRawBlob(next);
      }
    }).catch(() => {
      fallbackRenderImage(blob);
    });
  } else {
    fallbackRenderImage(blob);
  }
}

function fallbackRenderImage(blob) {
  const img = new Image();
  const url = URL.createObjectURL(blob);
  img.onload = () => {
    if (frameSafetyTimer) clearTimeout(frameSafetyTimer);
    if (canvas.width !== img.width || canvas.height !== img.height) {
      canvas.width = img.width;
      canvas.height = img.height;
    }
    ctx.drawImage(img, 0, 0);
    URL.revokeObjectURL(url);
    isRenderingFrame = false;
    if (pendingBlob) {
      const next = pendingBlob;
      pendingBlob = null;
      renderRawBlob(next);
    }
  };
  img.onerror = () => {
    if (frameSafetyTimer) clearTimeout(frameSafetyTimer);
    URL.revokeObjectURL(url);
    isRenderingFrame = false;
  };
  img.src = url;
}

function sendControl(ctrlObj) {
  if (!isConnected) return;

  // Send over WebRTC DataChannel if open (Direct, 0 Server Load, Ultra-low Latency)
  if (inputChannel && inputChannel.readyState === 'open') {
    inputChannel.send(JSON.stringify(ctrlObj));
    return;
  }

  // Fallback: Send over WebSocket Relay if WebRTC DataChannel is not open
  if (signalingWS && signalingWS.readyState === WebSocket.OPEN && currentTargetId) {
    signalingWS.send(JSON.stringify({
      action: 'data',
      id: currentClientSessionId,
      targetId: currentTargetId,
      payload: JSON.stringify(ctrlObj)
    }));
  }
}

function startPingLoop() {
  if (pingIntervalTimer) clearInterval(pingIntervalTimer);
  pingIntervalTimer = setInterval(() => {
    lastPingTime = performance.now();
    sendControl({ t: 'ping', ts: lastPingTime });
  }, 1200);
}

function openViewer() {
  isConnected = true;
  viewerContainer.style.display = 'flex';
  resetConnectButton();
  startPingLoop();
  const dock = document.getElementById('viewer-toolbar');
  if (dock) {
    dock.classList.add('dock-visible');
    setTimeout(() => {
      if (dock && !dock.matches(':hover') && !dock.matches(':focus-within')) {
        dock.classList.remove('dock-visible');
      }
    }, 3500);
  }
}

function closeViewer() {
  if (!isConnected && viewerContainer.style.display === 'none') return;
  isConnected = false;
  viewerContainer.style.display = 'none';
  if (pingIntervalTimer) {
    clearInterval(pingIntervalTimer);
    pingIntervalTimer = null;
  }

  // 1. Send close packet over input DataChannel first
  if (inputChannel && inputChannel.readyState === 'open') {
    try {
      inputChannel.send(JSON.stringify({ t: 'sys_cmd', cmd: 'close' }));
    } catch(e) {}
  }

  // 2. Send close action over Signaling WS
  if (signalingWS && signalingWS.readyState === WebSocket.OPEN && currentTargetId) {
    signalingWS.send(JSON.stringify({
      action: 'close',
      id: currentClientSessionId,
      targetId: currentTargetId
    }));
  }

  // 3. Close WebRTC channels & peer connection
  if (inputChannel) {
    try { inputChannel.close(); } catch(e) {}
    inputChannel = null;
  }
  if (videoChannel) {
    try { videoChannel.close(); } catch(e) {}
    videoChannel = null;
  }
  if (peerConnection) {
    try { peerConnection.close(); } catch(e) {}
    peerConnection = null;
  }

  if (document.fullscreenElement) {
    document.exitFullscreen().catch(() => {});
  }
  // Clear canvas
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  // Switch back to client view tab
  switchTab('client');
  resetConnectButton();
  fetchSessionStatus();
}

function resetConnectButton() {
  const btn = document.getElementById('btn-connect');
  btn.disabled = false;
  btn.innerHTML = '<span>🚀 Conectar e Assumir Controle</span>';
}

function toggleFullscreen() {
  if (!document.fullscreenElement) {
    viewerContainer.requestFullscreen().catch(() => {});
  } else {
    document.exitFullscreen().catch(() => {});
  }
}

function switchRemoteMonitor(monitorIdx) {
  sendControl({ t: 'mon_switch', mon: parseInt(monitorIdx) });
}

function setViewerQuality(mode) {
  if (mode === 'speed') {
    sendControl({ t: 'cfg', q: 45, fps: 60 });
  } else if (mode === 'balanced') {
    sendControl({ t: 'cfg', q: 65, fps: 30 });
  } else if (mode === 'hd') {
    sendControl({ t: 'cfg', q: 85, fps: 30 });
  }
}

async function openClipboardModal() {
  const text = await showModalPrompt('Colar Texto no Remoto', 'Digite ou cole o texto que deseja digitar na máquina remota:', '', '📋', 'Digitar Texto');
  if (text !== null && text.length > 0) {
    for (let i = 0; i < text.length; i++) {
      const ch = text[i];
      sendControl({ t: 'kd', k: ch, c: 'Key' + ch.toUpperCase(), kc: ch.charCodeAt(0) });
      sendControl({ t: 'ku', k: ch, c: 'Key' + ch.toUpperCase(), kc: ch.charCodeAt(0) });
    }
    await showModalAlert('Texto Enviado', 'Texto enviado e digitado no computador remoto!', '✅');
  }
}

function toggleChatDrawer() {
  const drawer = document.getElementById('chat-drawer');
  drawer.style.display = drawer.style.display === 'flex' ? 'none' : 'flex';
}

function sendChatMessage(e) {
  if (e) e.preventDefault();
  const input = document.getElementById('chat-input');
  const text = input.value.trim();
  if (!text) return;

  sendControl({ t: 'chat', text: text });
  appendChatMessage('Você', text);
  input.value = '';
}

function appendChatMessage(sender, text) {
  const container = document.getElementById('chat-messages');
  const msgEl = document.createElement('div');
  const isMe = sender === 'Você';
  msgEl.style.alignSelf = isMe ? 'flex-end' : 'flex-start';
  msgEl.style.background = isMe ? 'rgba(59, 130, 246, 0.4)' : 'rgba(255, 255, 255, 0.1)';
  msgEl.style.padding = '0.4rem 0.75rem';
  msgEl.style.borderRadius = '8px';
  msgEl.style.maxWidth = '85%';
  msgEl.innerHTML = `<strong style="color: ${isMe ? '#93c5fd' : '#86efac'}; font-size: 0.75rem;">${sender}</strong><div style="margin-top: 0.15rem;">${text}</div>`;
  container.appendChild(msgEl);
  container.scrollTop = container.scrollHeight;

  const drawer = document.getElementById('chat-drawer');
  if (drawer.style.display === 'none') {
    drawer.style.display = 'flex';
  }

  if (!isMe) {
    showChatToast(sender, text);
  }
}


function setupCanvasEvents() {
  canvas.addEventListener('contextmenu', (e) => e.preventDefault());

  canvas.addEventListener('mousemove', (e) => {
    if (!isConnected) return;
    const coords = getCanvasCoords(e);
    sendControl({ t: 'm', x: coords.x, y: coords.y });
  });

  canvas.addEventListener('mousedown', (e) => {
    if (!isConnected) return;
    isMouseDown = true;
    const coords = getCanvasCoords(e);
    sendControl({ t: 'm', x: coords.x, y: coords.y });
    sendControl({ t: 'md', b: e.button });
  });

  window.addEventListener('mouseup', (e) => {
    if (!isConnected || !isMouseDown) return;
    isMouseDown = false;
    sendControl({ t: 'mu', b: e.button });
  });

  canvas.addEventListener('wheel', (e) => {
    if (!isConnected) return;
    e.preventDefault();
    sendControl({ t: 'w', dy: e.deltaY > 0 ? -120 : 120 });
  }, { passive: false });
}

function setupKeyboardEvents() {
  window.addEventListener('keydown', (e) => {
    if (!isConnected || e.key === 'F12') return;
    if (document.activeElement && document.activeElement.tagName === 'INPUT') return;
    e.preventDefault();
    sendControl({ t: 'kd', k: e.key, c: e.code, kc: e.keyCode });
  });

  window.addEventListener('keyup', (e) => {
    if (!isConnected || e.key === 'F12') return;
    if (document.activeElement && document.activeElement.tagName === 'INPUT') return;
    e.preventDefault();
    sendControl({ t: 'ku', k: e.key, c: e.code, kc: e.keyCode });
  });
}

function sendSpecialKey(type) {
  if (!isConnected) return;

  if (type === 'ctrl_alt_del') {
    sendControl({ t: 'kd', k: 'Control', c: 'ControlLeft', kc: 17 });
    sendControl({ t: 'kd', k: 'Alt', c: 'AltLeft', kc: 18 });
    sendControl({ t: 'kd', k: 'Delete', c: 'Delete', kc: 46 });
    setTimeout(() => {
      sendControl({ t: 'ku', k: 'Delete', c: 'Delete', kc: 46 });
      sendControl({ t: 'ku', k: 'Alt', c: 'AltLeft', kc: 18 });
      sendControl({ t: 'ku', k: 'Control', c: 'ControlLeft', kc: 17 });
    }, 120);
  } else if (type === 'task_manager') {
    sendControl({ t: 'kd', k: 'Control', c: 'ControlLeft', kc: 17 });
    sendControl({ t: 'kd', k: 'Shift', c: 'ShiftLeft', kc: 16 });
    sendControl({ t: 'kd', k: 'Escape', c: 'Escape', kc: 27 });
    setTimeout(() => {
      sendControl({ t: 'ku', k: 'Escape', c: 'Escape', kc: 27 });
      sendControl({ t: 'ku', k: 'Shift', c: 'ShiftLeft', kc: 16 });
      sendControl({ t: 'ku', k: 'Control', c: 'ControlLeft', kc: 17 });
    }, 120);
  } else if (type === 'win_d') {
    sendControl({ t: 'kd', k: 'Meta', c: 'MetaLeft', kc: 91 });
    sendControl({ t: 'kd', k: 'd', c: 'KeyD', kc: 68 });
    setTimeout(() => {
      sendControl({ t: 'ku', k: 'd', c: 'KeyD', kc: 68 });
      sendControl({ t: 'ku', k: 'Meta', c: 'MetaLeft', kc: 91 });
    }, 120);
  } else if (type === 'alt_tab') {
    sendControl({ t: 'kd', k: 'Alt', c: 'AltLeft', kc: 18 });
    sendControl({ t: 'kd', k: 'Tab', c: 'Tab', kc: 9 });
    setTimeout(() => {
      sendControl({ t: 'ku', k: 'Tab', c: 'Tab', kc: 9 });
      sendControl({ t: 'ku', k: 'Alt', c: 'AltLeft', kc: 18 });
    }, 120);
  } else if (type === 'win_l') {
    sendControl({ t: 'kd', k: 'Meta', c: 'MetaLeft', kc: 91 });
    sendControl({ t: 'kd', k: 'l', c: 'KeyL', kc: 76 });
    setTimeout(() => {
      sendControl({ t: 'ku', k: 'l', c: 'KeyL', kc: 76 });
      sendControl({ t: 'ku', k: 'Meta', c: 'MetaLeft', kc: 91 });
    }, 120);
  }
}

// Saved Connections with Passwords and WoL (1-Click Reconnect & Wake-on-LAN)
function loadRecentConnections() {
  const raw = localStorage.getItem('ra_saved_devices');
  const list = raw ? JSON.parse(raw) : [];
  const container = document.getElementById('recent-list');

  if (list.length === 0) {
    container.innerHTML = '<span style="font-size: 0.85rem; color: var(--text-muted);">Nenhum computador salvo ainda.</span>';
    return;
  }

  container.innerHTML = '';
  list.forEach((item, idx) => {
    const chip = document.createElement('div');
    chip.className = 'recent-chip';
    const label = item.alias ? `${item.alias} (${formatID(item.id)})` : formatID(item.id);
    
    let wolHtml = '';
    if (item.mac) {
      wolHtml = `<button class="btn-wol-mini" onclick="event.stopPropagation(); triggerLocalWoL('${item.mac}', '${item.alias || item.id}')" title="Enviar Wake-on-LAN (Ligar este computador à distância)" style="background: rgba(245, 158, 11, 0.2); border: 1px solid rgba(245, 158, 11, 0.4); color: #fcd34d; padding: 0.2rem 0.5rem; border-radius: 6px; font-size: 0.75rem; cursor: pointer; margin-left: 0.35rem; font-weight: 700;">⚡ Ligar (WoL)</button>`;
    } else {
      wolHtml = `<button class="btn-wol-mini" onclick="event.stopPropagation(); promptSetDeviceMAC(${idx})" title="Cadastrar MAC Address para Wake-on-LAN" style="background: rgba(255, 255, 255, 0.06); border: 1px solid rgba(255, 255, 255, 0.12); color: var(--text-muted); padding: 0.2rem 0.4rem; border-radius: 6px; font-size: 0.7rem; cursor: pointer; margin-left: 0.35rem;">+ WoL</button>`;
    }

    chip.innerHTML = `
      <div style="display: flex; align-items: center; gap: 0.4rem; flex-wrap: wrap;">
        <span>💻 <strong>${label}</strong></span>
        ${wolHtml}
        <span class="btn-connect-chip" style="font-size: 0.75rem; background: rgba(59,130,246,0.3); padding: 0.2rem 0.5rem; border-radius: 6px; cursor: pointer; font-weight: 700; color: #93c5fd;">🚀 Conectar</span>
        <span class="btn-remove-recent" title="Remover dos Recentes" style="margin-left: 0.3rem; opacity: 0.6; cursor: pointer; padding: 0.1rem 0.3rem;">✕</span>
      </div>
    `;
    
    chip.querySelector('.btn-connect-chip').onclick = (e) => {
      e.stopPropagation();
      document.getElementById('target-id').value = formatID(item.id);
      document.getElementById('target-pwd').value = item.password || '';
      connectToRemote();
    };

    chip.querySelector('.btn-remove-recent').onclick = (e) => {
      e.stopPropagation();
      removeRecentConnection(idx);
    };

    chip.onclick = () => {
      document.getElementById('target-id').value = formatID(item.id);
      document.getElementById('target-pwd').value = item.password || '';
      connectToRemote();
    };
    container.appendChild(chip);
  });
}

async function triggerLocalWoL(mac, name) {
  if (!mac) return;
  try {
    const res = await fetch('/api/send-wol', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ mac: mac })
    });
    const data = await res.json();
    if (data.status === 'ok') {
      await showModalAlert('Wake-on-LAN', `⚡ Pacote Magic Packet Wake-on-LAN disparado para ${name} (${mac})!\n\nSe a placa-mãe/BIOS estiver com WoL ativado, o computador irá ligar agora.`, '⚡');
    } else {
      await showModalAlert('Falha no WoL', 'Falha ao enviar pacote WoL: ' + (data.message || 'Erro desconhecido'), '❌');
    }
  } catch (err) {
    await showModalAlert('Erro', 'Erro de comunicação ao enviar Wake-on-LAN.', '❌');
  }
}

async function promptSetDeviceMAC(idx) {
  let list = JSON.parse(localStorage.getItem('ra_saved_devices') || '[]');
  if (!list[idx]) return;
  const current = list[idx].mac || '';
  const input = await showModalPrompt('Endereço MAC (WoL)', `Digite o endereço MAC físico do computador ${list[idx].alias || list[idx].id} (ex: 00:1A:2B:3C:4D:5E):`, current, '⚡');
  if (input === null) return;
  list[idx].mac = input.trim().toUpperCase();
  localStorage.setItem('ra_saved_devices', JSON.stringify(list));
  loadRecentConnections();
}

function removeRecentConnection(idx) {
  let list = JSON.parse(localStorage.getItem('ra_saved_devices') || '[]');
  list.splice(idx, 1);
  localStorage.setItem('ra_saved_devices', JSON.stringify(list));
  loadRecentConnections();
}

function saveRecentConnection(id, password, alias, mac) {
  let list = JSON.parse(localStorage.getItem('ra_saved_devices') || '[]');
  const existing = list.find((x) => x.id === id);
  const finalMac = mac || (existing ? existing.mac : '');
  const finalAlias = alias || (existing ? existing.alias : '');
  list = [{ id: id, password: password, alias: finalAlias, mac: finalMac }, ...list.filter((x) => x.id !== id)].slice(0, 8);
  localStorage.setItem('ra_saved_devices', JSON.stringify(list));
  loadRecentConnections();
}

async function openClipboardModal() {
  const text = await showModalPrompt(
    'Colar Texto Remoto',
    'Digite ou cole o texto que deseja injetar diretamente no computador remoto:',
    '',
    '📋',
    'Colar Texto',
    'Cancelar'
  );
  if (text) {
    sendControl({ t: 'clip', text: text });
    await showModalAlert('Texto Injetado', 'Texto enviado e colado com sucesso no computador remoto!', '✅');
  }
}

function triggerFileUpload() {
  const input = document.getElementById('viewer-file-input');
  if (input) input.click();
}

async function handleViewerFileSelect(files) {
  if (!files || files.length === 0) return;
  for (const file of files) {
    await sendFileInChunks(file);
  }
  const input = document.getElementById('viewer-file-input');
  if (input) input.value = '';
}

async function sendFileInChunks(file) {
  const chunkSize = 48 * 1024; // 48KB per chunk
  const totalChunks = Math.ceil(file.size / chunkSize);

  sendControl({ t: 'file_start', file_name: file.name, file_size: file.size });

  for (let i = 0; i < totalChunks; i++) {
    const start = i * chunkSize;
    const end = Math.min(file.size, start + chunkSize);
    const slice = file.slice(start, end);

    const base64Chunk = await new Promise((resolve) => {
      const r = new FileReader();
      r.onload = () => {
        const b64 = r.result.split(',')[1];
        resolve(b64);
      };
      r.readAsDataURL(slice);
    });

    sendControl({
      t: 'file_chunk',
      file_name: file.name,
      chunk: base64Chunk,
      seq: i + 1
    });

    await new Promise(r => setTimeout(r, 10));
  }

  sendControl({ t: 'file_end', file_name: file.name });
  await showModalAlert('Arquivo Enviado', `Arquivo "${file.name}" transmitido com sucesso para a pasta Downloads\\RemoteAccess_Transfers do computador remoto!`, '📁');
}

window.addEventListener('DOMContentLoaded', () => {
  const cWrapper = document.getElementById('canvas-wrapper');
  if (cWrapper) {
    cWrapper.addEventListener('dragover', (e) => {
      e.preventDefault();
      e.stopPropagation();
    });
    cWrapper.addEventListener('drop', async (e) => {
      e.preventDefault();
      e.stopPropagation();
      if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
        for (const file of e.dataTransfer.files) {
          await sendFileInChunks(file);
        }
      }
    });
  }
});

window.addEventListener('beforeunload', () => {
  try {
    if (isConnected && currentTargetId) {
      if (inputChannel && inputChannel.readyState === 'open') {
        inputChannel.send(JSON.stringify({ t: 'sys_cmd', cmd: 'close' }));
      }
      if (signalingWS && signalingWS.readyState === WebSocket.OPEN) {
        signalingWS.send(JSON.stringify({
          action: 'close',
          id: currentClientSessionId,
          targetId: currentTargetId
        }));
      }
    }
    navigator.sendBeacon('/api/app-close');
  } catch (e) {}
});

