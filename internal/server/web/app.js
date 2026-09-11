// RemoteAccess Client & Host Management

let myHostInfo = {
  id: '',
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
  connectSignaling();
  setupCanvasEvents();
  setupKeyboardEvents();

  setInterval(() => {
    fetchHostInfo();
    if (logsModalOpen) refreshLogs();
  }, 2500);
});

function switchTab(tab) {
  currentTab = tab;
  document.getElementById('tab-host-btn').classList.toggle('active', tab === 'host');
  document.getElementById('tab-client-btn').classList.toggle('active', tab === 'client');
  document.getElementById('host-view').style.display = tab === 'host' ? 'block' : 'none';
  document.getElementById('client-view').style.display = tab === 'client' ? 'block' : 'none';
}

async function fetchHostInfo() {
  try {
    const res = await fetch('/api/host-info');
    const data = await res.json();
    myHostInfo.id = data.id;
    myHostInfo.rawPwd = data.password;
    myHostInfo.autoStart = data.auto_start;
    myHostInfo.signalingURL = data.signaling_url || '';
    myHostInfo.cloudStatus = data.cloud_status || 'local';
    
    document.getElementById('my-id').innerText = formatID(data.id);
    document.getElementById('autostart-toggle').checked = !!data.auto_start;
    updatePasswordDisplay();
    updateNetworkBadge(data.cloud_status, data.signaling_url);
  } catch (err) {
    console.error('Failed to fetch host info:', err);
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
    const container = document.getElementById('logs-container');
    if (data.logs && data.logs.length > 0) {
      container.innerText = data.logs.join('\n');
      container.scrollTop = container.scrollHeight;
    } else {
      container.innerText = 'Nenhum log registrado ainda.';
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
          renderBase64Frame(msg.payload);
          break;

        case 'close':
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
      { urls: 'stun:stun.cloudflare.com:3478' }
    ]
  };

  peerConnection = new RTCPeerConnection(config);
  inputChannel = peerConnection.createDataChannel('input', { ordered: true });
  videoChannel = peerConnection.createDataChannel('video', { maxRetransmits: 0, ordered: false });

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
        document.getElementById('stat-latency').innerText = `⚡ ${rtt} ms (P2P)`;
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
    document.getElementById('stat-fps').innerText = `🎥 ${frameCount} fps`;
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
    document.getElementById('stat-fps').innerText = `🎥 ${frameCount} fps (P2P)`;
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
  setInterval(() => {
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
  if (peerConnection) {
    peerConnection.close();
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

function setupCanvasEvents() {
  canvas.addEventListener('contextmenu', (e) => e.preventDefault());

  canvas.addEventListener('mousemove', (e) => {
    if (!isConnected) return;
    const rect = canvas.getBoundingClientRect();
    const ratioX = (e.clientX - rect.left) / rect.width;
    const ratioY = (e.clientY - rect.top) / rect.height;

    sendControl({ t: 'm', x: ratioX, y: ratioY });
  });

  canvas.addEventListener('mousedown', (e) => {
    if (!isConnected) return;
    sendControl({ t: 'md', b: e.button });
  });

  canvas.addEventListener('mouseup', (e) => {
    if (!isConnected) return;
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
    e.preventDefault();
    sendControl({ t: 'kd', k: e.key, c: e.code, kc: e.keyCode });
  });

  window.addEventListener('keyup', (e) => {
    if (!isConnected || e.key === 'F12') return;
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
    }, 100);
  } else if (type === 'win_d') {
    sendControl({ t: 'kd', k: 'Meta', c: 'MetaLeft', kc: 91 });
    sendControl({ t: 'kd', k: 'd', c: 'KeyD', kc: 68 });
    setTimeout(() => {
      sendControl({ t: 'ku', k: 'd', c: 'KeyD', kc: 68 });
      sendControl({ t: 'ku', k: 'Meta', c: 'MetaLeft', kc: 91 });
    }, 100);
  } else if (type === 'alt_tab') {
    sendControl({ t: 'kd', k: 'Alt', c: 'AltLeft', kc: 18 });
    sendControl({ t: 'kd', k: 'Tab', c: 'Tab', kc: 9 });
    setTimeout(() => {
      sendControl({ t: 'ku', k: 'Tab', c: 'Tab', kc: 9 });
      sendControl({ t: 'ku', k: 'Alt', c: 'AltLeft', kc: 18 });
    }, 100);
  }
}

// Saved Connections with Passwords (1-Click Reconnect)
function loadRecentConnections() {
  const raw = localStorage.getItem('ra_saved_devices');
  const list = raw ? JSON.parse(raw) : [];
  const container = document.getElementById('recent-list');

  if (list.length === 0) {
    container.innerHTML = '<span style="font-size: 0.85rem; color: var(--text-muted);">Nenhum computador salvo ainda.</span>';
    return;
  }

  container.innerHTML = '';
  list.forEach((item) => {
    const chip = document.createElement('div');
    chip.className = 'recent-chip';
    chip.innerHTML = `💻 <strong>${formatID(item.id)}</strong> <span style="font-size: 0.75rem; background: rgba(59,130,246,0.3); padding: 0.15rem 0.4rem; border-radius: 4px; margin-left: 0.3rem;">⚡ Conectar</span>`;
    chip.onclick = () => {
      document.getElementById('target-id').value = formatID(item.id);
      document.getElementById('target-pwd').value = item.password || '';
      connectToRemote();
    };
    container.appendChild(chip);
  });
}

function saveRecentConnection(id, password) {
  let list = JSON.parse(localStorage.getItem('ra_saved_devices') || '[]');
  list = [{ id: id, password: password }, ...list.filter((x) => x.id !== id)].slice(0, 5);
  localStorage.setItem('ra_saved_devices', JSON.stringify(list));
  loadRecentConnections();
}
