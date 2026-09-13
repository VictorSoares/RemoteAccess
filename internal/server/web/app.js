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

// Initialize application
window.addEventListener('DOMContentLoaded', async () => {
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
  }, 2000);
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
    
    document.getElementById('autostart-toggle').checked = !!data.auto_start;
    const saveLogEl = document.getElementById('savelog-toggle');
    if (saveLogEl) saveLogEl.checked = !!data.save_log_file;
    updatePasswordDisplay();
    updateNetworkBadge(data.cloud_status, data.signaling_url);
  } catch (err) {
    console.error('Failed to fetch host info:', err);
  }
}

async function openCustomAliasModal() {
  const current = myHostInfo.alias || '';
  const newAlias = prompt('Digite o novo apelido/nome de identificação para este computador:', current);
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
      alert('Apelido do computador atualizado com sucesso!');
    }
  } catch (err) {
    alert('Erro ao salvar novo apelido.');
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
        alert('Gravação de arquivo de log em disco ativada (%APPDATA%\\RemoteAccess\\remoteaccess.log).');
      } else {
        alert('Gravação de arquivo de log desativada. A pasta permanecerá limpa.');
      }
    }
  } catch (err) {
    alert('Erro ao salvar configuração de log.');
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
      
      const monSelect = document.getElementById('viewer-monitor-select');
      monSelect.innerHTML = '';
      for (let i = 0; i < data.monitors; i++) {
        const opt = document.createElement('option');
        opt.value = i;
        opt.innerText = `🖥️ Monitor ${i + 1}`;
        monSelect.appendChild(opt);
      }
    }
  } catch (err) {}
}

let hostChatOpen = false;
let lastHostMsgCount = 0;

async function fetchSessionStatus() {
  try {
    const res = await fetch('/api/session-status');
    const data = await res.json();
    const box = document.getElementById('active-session-box');
    if (data.active) {
      box.style.display = 'flex';
      document.getElementById('host-session-client-id').innerText = data.client_id || 'Cliente';
      const m = Math.floor(data.duration / 60).toString().padStart(2, '0');
      const s = (data.duration % 60).toString().padStart(2, '0');
      document.getElementById('host-session-duration').innerText = `${m}:${s}`;
      await refreshHostChat();
    } else {
      box.style.display = 'none';
      lastHostMsgCount = 0;
    }
  } catch (err) {}
}

function playChatChime() {
  try {
    const ctx = new (window.AudioContext || window.webkitAudioContext)();
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    osc.type = 'sine';
    osc.frequency.setValueAtTime(587.33, ctx.currentTime);
    osc.frequency.exponentialRampToValueAtTime(880, ctx.currentTime + 0.12);
    gain.gain.setValueAtTime(0.2, ctx.currentTime);
    gain.gain.exponentialRampToValueAtTime(0.001, ctx.currentTime + 0.35);
    osc.connect(gain);
    gain.connect(ctx.destination);
    osc.start();
    osc.stop(ctx.currentTime + 0.35);
  } catch(e) {}
}

let toastTimer = null;
function showChatToast(sender, text) {
  const toast = document.getElementById('chat-toast-popup');
  if (!toast) return;
  document.getElementById('chat-toast-title').innerText = `💬 Mensagem de ${sender}`;
  document.getElementById('chat-toast-text').innerText = text;
  toast.style.display = 'flex';
  playChatChime();

  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    hideChatToast();
  }, 7000);
}

function hideChatToast() {
  const toast = document.getElementById('chat-toast-popup');
  if (toast) toast.style.display = 'none';
}

function handleToastClick() {
  hideChatToast();
  if (isConnected) {
    const drawer = document.getElementById('chat-drawer');
    if (drawer) drawer.style.display = 'flex';
  } else {
    hostChatOpen = true;
    const chatSec = document.getElementById('host-chat-container');
    if (chatSec) chatSec.style.display = 'flex';
    document.getElementById('host-chat-badge').style.display = 'none';
  }
}

async function refreshHostChat() {
  try {
    const res = await fetch('/api/chat-messages');
    const data = await res.json();
    if (data.messages) {
      const container = document.getElementById('host-chat-messages');
      if (data.messages.length !== lastHostMsgCount) {
        const isInitial = lastHostMsgCount === 0;
        const previousCount = lastHostMsgCount;
        lastHostMsgCount = data.messages.length;

        container.innerHTML = '';
        data.messages.forEach(msg => {
          const isMe = msg.sender.includes('Host') || msg.sender.includes('Você');
          const bubble = document.createElement('div');
          bubble.className = `msg-bubble ${isMe ? 'msg-mine' : 'msg-other'}`;
          bubble.innerHTML = `<strong style="font-size: 0.72rem; opacity: 0.85;">${msg.sender}</strong><div>${msg.text}</div><div class="msg-meta">${msg.time}</div>`;
          container.appendChild(bubble);
        });
        container.scrollTop = container.scrollHeight;

        if (!isInitial && data.messages.length > previousCount) {
          const latestMsg = data.messages[data.messages.length - 1];
          const isMe = latestMsg.sender.includes('Host') || latestMsg.sender.includes('Você');
          if (!isMe) {
            showChatToast(latestMsg.sender, latestMsg.text);
            // Auto expand chat section so user sees it right away
            hostChatOpen = true;
            const chatSec = document.getElementById('host-chat-container');
            if (chatSec) chatSec.style.display = 'flex';
            document.getElementById('host-chat-badge').style.display = 'none';
          }
        }
      }
    }
  } catch (err) {}
}

function toggleHostChat() {
  hostChatOpen = !hostChatOpen;
  const chatSec = document.getElementById('host-chat-container');
  chatSec.style.display = hostChatOpen ? 'flex' : 'none';
  if (hostChatOpen) {
    document.getElementById('host-chat-badge').style.display = 'none';
    const container = document.getElementById('host-chat-messages');
    container.scrollTop = container.scrollHeight;
  }
}

async function sendHostChatMessage(e) {
  if (e) e.preventDefault();
  const input = document.getElementById('host-chat-input');
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
  try {
    await fetch('/api/kick-session', { method: 'POST' });
    document.getElementById('active-session-box').style.display = 'none';
  } catch (err) {}
}

function sendSystemAction(action) {
  sendControl({ t: 'sys_cmd', cmd: action });
}

function sendPowerAction(action) {
  if (!isConnected) return;
  if (action === 'reboot') {
    if (confirm('⚠️ Deseja realmente REINICIAR o computador remoto?\n\nO sistema será reiniciado em 5 segundos e você poderá reconectar assim que ele inicializar.')) {
      sendControl({ t: 'sys_cmd', cmd: 'reboot' });
      alert('Comando de reinicialização enviado ao computador remoto.');
    }
  } else if (action === 'shutdown') {
    if (confirm('🛑 ATENÇÃO: Deseja realmente DESLIGAR o computador remoto?\n\nEle será desligado completamente. Para ligá-lo novamente à distância, será necessário utilizar Wake-on-LAN (WoL).')) {
      sendControl({ t: 'sys_cmd', cmd: 'shutdown' });
      alert('Comando de desligamento enviado ao computador remoto.');
    }
  } else if (action === 'suspend') {
    if (confirm('🌙 Deseja colocar o computador remoto em modo de SUSPENSÃO (Sleep/Repouso)?')) {
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
  const newPwd = prompt('Digite a nova senha fixa permanente que deseja usar para este computador (mínimo 3 caracteres):', myHostInfo.rawPwd);
  if (!newPwd || newPwd.trim().length < 3) {
    if (newPwd !== null) alert('A senha precisa ter pelo menos 3 caracteres.');
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
      alert('Senha fixa salva com sucesso! Ela nunca mais mudará.');
    }
  } catch (err) {
    alert('Erro ao salvar nova senha.');
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
        alert('RemoteAccess agora iniciará automaticamente em segundo plano com o Windows!');
      } else {
        alert('Inicialização automática desativada.');
      }
    }
  } catch (err) {
    alert('Erro ao atualizar inicialização automática.');
    e.target.checked = !enable;
  }
}

function copyToClipboard(elementId) {
  const el = document.getElementById(elementId);
  const text = elementId === 'my-pwd' ? myHostInfo.rawPwd : el.innerText.replace(/\s+/g, '');
  navigator.clipboard.writeText(text).then(() => {
    alert('Copiado para a área de transferência: ' + text);
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
  const newUrl = prompt('Digite o endereço do seu servidor Render (ex: https://remoteaccess-ltwx.onrender.com ou deixe em branco para modo local):', current);
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
      alert('Servidor de Nuvem configurado!');
      await fetchHostInfo();
      connectSignaling();
    }
  } catch (err) {
    alert('Erro ao salvar servidor de sinalização.');
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
          alert(msg.message || 'Erro de conexão.');
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
                const rtt = Math.round(performance.now() - ctrl.ts);
                const latEl = document.getElementById('stat-latency');
                if (latEl) latEl.innerText = `⚡ ${rtt} ms`;
                const badgeEl = document.getElementById('hud-status-badge');
                if (badgeEl) badgeEl.innerHTML = `<span class="hud-dot" style="background-color: #38bdf8; box-shadow: 0 0 6px #38bdf8;"></span> Nuvem Relay`;
              } else if (ctrl.t === 'chat') {
                appendChatMessage('Remoto', ctrl.text);
              }
            } catch(e) {}
          } else {
            renderBase64Frame(msg.payload);
          }
          break;

        case 'close':
          alert('A sessão remota foi encerrada pelo computador remoto.');
          closeViewer();
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
    alert('Por favor, informe o ID e a Senha.');
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

  // 1. Send authentication request
  if (signalingWS && signalingWS.readyState === WebSocket.OPEN) {
    signalingWS.send(JSON.stringify({
      action: 'connect',
      id: currentClientSessionId,
      targetId: rawTargetId,
      password: targetPwd
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

  inputChannel.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data);
      if (msg.t === 'pong') {
        const rtt = Math.round(performance.now() - msg.ts);
        const latEl = document.getElementById('stat-latency');
        if (latEl) latEl.innerText = `⚡ ${rtt} ms`;
        const badgeEl = document.getElementById('hud-status-badge');
        if (badgeEl) badgeEl.innerHTML = `<span class="hud-dot"></span> P2P Direct`;
      } else if (msg.t === 'chat') {
        appendChatMessage('Remoto', msg.text);
      }
    } catch (e) {}
  };

  videoChannel.onmessage = async (event) => {
    renderRawBlob(event.data);
  };
}

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

function renderRawBlob(blobData) {
  frameCount++;
  const now = performance.now();
  if (now - lastFpsTime >= 1000) {
    const fpsEl = document.getElementById('stat-fps');
    if (fpsEl) fpsEl.innerText = `🎥 ${frameCount} FPS`;
    frameCount = 0;
    lastFpsTime = now;
  }

  const blob = new Blob([blobData], { type: 'image/jpeg' });
  createImageBitmap(blob).then((imgBitmap) => {
    if (canvas.width !== imgBitmap.width || canvas.height !== imgBitmap.height) {
      canvas.width = imgBitmap.width;
      canvas.height = imgBitmap.height;
    }
    ctx.drawImage(imgBitmap, 0, 0);
    imgBitmap.close();
  }).catch(() => {
    const img = new Image();
    const url = URL.createObjectURL(blob);
    img.onload = () => {
      if (canvas.width !== img.width || canvas.height !== img.height) {
        canvas.width = img.width;
        canvas.height = img.height;
      }
      ctx.drawImage(img, 0, 0);
      URL.revokeObjectURL(url);
    };
    img.src = url;
  });
}

function sendControl(ctrlObj) {
  if (!isConnected) return;

  if (inputChannel && inputChannel.readyState === 'open') {
    inputChannel.send(JSON.stringify(ctrlObj));
    return;
  }

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
  }, 2000);
}

function openViewer() {
  isConnected = true;
  viewerContainer.style.display = 'flex';
  resetConnectButton();
}

function closeViewer() {
  isConnected = false;
  viewerContainer.style.display = 'none';
  if (pingIntervalTimer) {
    clearInterval(pingIntervalTimer);
    pingIntervalTimer = null;
  }
  if (peerConnection) {
    try { peerConnection.close(); } catch(e) {}
    peerConnection = null;
  }
  if (signalingWS && signalingWS.readyState === WebSocket.OPEN && currentTargetId) {
    signalingWS.send(JSON.stringify({
      action: 'close',
      id: currentClientSessionId,
      targetId: currentTargetId
    }));
  }
  if (document.fullscreenElement) {
    document.exitFullscreen().catch(() => {});
  }
  resetConnectButton();
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

function openClipboardModal() {
  const text = prompt('Digite ou cole o texto que deseja enviar para a máquina remota:');
  if (text !== null && text.length > 0) {
    for (let i = 0; i < text.length; i++) {
      const ch = text[i];
      sendControl({ t: 'kd', k: ch, c: 'Key' + ch.toUpperCase(), kc: ch.charCodeAt(0) });
      sendControl({ t: 'ku', k: ch, c: 'Key' + ch.toUpperCase(), kc: ch.charCodeAt(0) });
    }
    alert('Texto enviado para o computador remoto!');
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
      alert(`⚡ Pacote Magic Packet Wake-on-LAN disparado para ${name} (${mac})!\n\nSe a placa-mãe/BIOS estiver com WoL ativado, o computador irá ligar agora.`);
    } else {
      alert('Falha ao enviar pacote WoL: ' + (data.message || 'Erro desconhecido'));
    }
  } catch (err) {
    alert('Erro de comunicação ao enviar Wake-on-LAN.');
  }
}

function promptSetDeviceMAC(idx) {
  let list = JSON.parse(localStorage.getItem('ra_saved_devices') || '[]');
  if (!list[idx]) return;
  const current = list[idx].mac || '';
  const input = prompt(`Digite o endereço MAC físico do computador ${list[idx].alias || list[idx].id} (ex: 00:1A:2B:3C:4D:5E):`, current);
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

window.addEventListener('beforeunload', () => {
  try {
    navigator.sendBeacon('/api/app-close');
  } catch (e) {}
});

