// RemoteAccess Client & Host Management

let myHostInfo = {
  id: '',
  pwd: '',
  rawPwd: '',
  autoStart: false,
  signalingURL: ''
};

let signalingWS = null;
let peerConnection = null;
let inputChannel = null;
let videoChannel = null;
let isConnected = false;
let currentTab = 'host';
let pwdVisible = false;

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
    
    document.getElementById('my-id').innerText = formatID(data.id);
    document.getElementById('autostart-toggle').checked = !!data.auto_start;
    updatePasswordDisplay();
  } catch (err) {
    console.error('Failed to fetch host info:', err);
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
      alert('Senha fixa salva com sucesso! Ela será mantida mesmo ao reiniciar o PC.');
      
      // Update signaling with new password
      if (signalingWS && signalingWS.readyState === WebSocket.OPEN) {
        signalingWS.send(JSON.stringify({
          action: 'register',
          id: myHostInfo.id,
          password: myHostInfo.rawPwd
        }));
      }
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
        alert('RemoteAccess agora iniciará automaticamente em segundo plano sempre que o Windows for ligado!');
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
  const newUrl = prompt('Digite o endereço do seu servidor de sinalização na nuvem (ex: wss://seu-app.onrender.com/ws ou deixe em branco para usar o padrão local):', current);
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
      alert('Endereço do servidor atualizado com sucesso! Reconectando...');
      if (signalingWS) {
        signalingWS.close();
      } else {
        connectSignaling();
      }
    }
  } catch (err) {
    alert('Erro ao salvar servidor de sinalização.');
  }
}

// Signaling WebSocket Connection
function connectSignaling() {
  let wsUrl = '';
  if (myHostInfo.signalingURL && myHostInfo.signalingURL.trim() !== '') {
    wsUrl = myHostInfo.signalingURL.trim();
    if (wsUrl.startsWith('http://')) wsUrl = wsUrl.replace('http://', 'ws://');
    if (wsUrl.startsWith('https://')) wsUrl = wsUrl.replace('https://', 'wss://');
    if (!wsUrl.endsWith('/ws')) wsUrl = wsUrl.replace(/\/$/, '') + '/ws';
  } else {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    wsUrl = `${protocol}//${window.location.host}/ws`;
  }

  try {
    signalingWS = new WebSocket(wsUrl);
  } catch (err) {
    document.getElementById('status-indicator').className = 'status-dot';
    document.getElementById('status-text').innerText = 'Erro no Relay';
    return;
  }

  signalingWS.onopen = () => {
    document.getElementById('status-indicator').className = 'status-dot online';
    document.getElementById('status-text').innerText = 'Online / Nuvem Conectada';

    // Register local host
    if (myHostInfo.id) {
      signalingWS.send(JSON.stringify({
        action: 'register',
        id: myHostInfo.id,
        password: myHostInfo.rawPwd
      }));
    }
  };

  signalingWS.onmessage = async (event) => {
    const msg = JSON.parse(event.data);

    switch (msg.action) {
      case 'status':
        console.log('[Signaling Status]', msg.message);
        break;

      case 'error':
        alert(msg.message || 'Erro de conexão.');
        resetConnectButton();
        break;

      case 'answer':
        // Client receives answer from host
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

      case 'close':
        closeViewer();
        break;
    }
  };

  signalingWS.onclose = () => {
    document.getElementById('status-indicator').className = 'status-dot';
    document.getElementById('status-text').innerText = 'Reconectando...';
    setTimeout(connectSignaling, 3000);
  };
}

// Client: Connect to Remote Host
async function connectToRemote(e) {
  e.preventDefault();
  const rawTargetId = document.getElementById('target-id').value.replace(/\D/g, '');
  const targetPwd = document.getElementById('target-pwd').value;

  if (!rawTargetId || !targetPwd) {
    alert('Por favor, informe o ID e a Senha.');
    return;
  }

  const btn = document.getElementById('btn-connect');
  btn.disabled = true;
  btn.innerHTML = '<span>⏳ Conectando...</span>';

  saveRecentConnection(rawTargetId);

  // Setup WebRTC Peer Connection
  const config = {
    iceServers: [
      { urls: 'stun:stun.l.google.com:19302' },
      { urls: 'stun:stun1.l.google.com:19302' },
      { urls: 'stun:stun.cloudflare.com:3478' }
    ]
  };

  peerConnection = new RTCPeerConnection(config);

  // Create Data Channels
  inputChannel = peerConnection.createDataChannel('input', { ordered: true });
  videoChannel = peerConnection.createDataChannel('video', { maxRetransmits: 0, ordered: false });

  setupDataChannels(rawTargetId);

  peerConnection.onicecandidate = (event) => {
    if (event.candidate && signalingWS) {
      signalingWS.send(JSON.stringify({
        action: 'candidate',
        targetId: rawTargetId,
        candidate: event.candidate.toJSON()
      }));
    }
  };

  // Create Offer
  const offer = await peerConnection.createOffer();
  await peerConnection.setLocalDescription(offer);

  // Send Offer through Signaling Server
  signalingWS.send(JSON.stringify({
    action: 'offer',
    targetId: rawTargetId,
    password: targetPwd,
    sdp: offer.sdp
  }));
}

function setupDataChannels(targetId) {
  inputChannel.onopen = () => {
    console.log('[Client] Input DataChannel open');
    openViewer();
    startPingLoop();
  };

  inputChannel.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data);
      if (msg.t === 'pong') {
        const rtt = Math.round(performance.now() - msg.ts);
        document.getElementById('stat-latency').innerText = `⚡ ${rtt} ms`;
      }
    } catch (e) {}
  };

  videoChannel.onopen = () => {
    console.log('[Client] Video DataChannel open');
  };

  videoChannel.onmessage = async (event) => {
    frameCount++;
    const now = performance.now();
    if (now - lastFpsTime >= 1000) {
      document.getElementById('stat-fps').innerText = `🎥 ${frameCount} fps`;
      frameCount = 0;
      lastFpsTime = now;
    }

    const blob = new Blob([event.data], { type: 'image/jpeg' });
    try {
      const imgBitmap = await createImageBitmap(blob);
      if (canvas.width !== imgBitmap.width || canvas.height !== imgBitmap.height) {
        canvas.width = imgBitmap.width;
        canvas.height = imgBitmap.height;
      }
      ctx.drawImage(imgBitmap, 0, 0);
      imgBitmap.close();
    } catch (err) {
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
    }
  };
}

function startPingLoop() {
  setInterval(() => {
    if (inputChannel && inputChannel.readyState === 'open') {
      lastPingTime = performance.now();
      inputChannel.send(JSON.stringify({
        t: 'ping',
        ts: lastPingTime
      }));
    }
  }, 2000);
}

// Viewer UI Management
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

// Canvas & Input Event Listeners
function setupCanvasEvents() {
  canvas.addEventListener('contextmenu', (e) => e.preventDefault());

  canvas.addEventListener('mousemove', (e) => {
    if (!isConnected || !inputChannel || inputChannel.readyState !== 'open') return;

    const rect = canvas.getBoundingClientRect();
    const ratioX = (e.clientX - rect.left) / rect.width;
    const ratioY = (e.clientY - rect.top) / rect.height;

    inputChannel.send(JSON.stringify({
      t: 'm',
      x: ratioX,
      y: ratioY
    }));
  });

  canvas.addEventListener('mousedown', (e) => {
    if (!isConnected || !inputChannel || inputChannel.readyState !== 'open') return;
    inputChannel.send(JSON.stringify({
      t: 'md',
      b: e.button
    }));
  });

  canvas.addEventListener('mouseup', (e) => {
    if (!isConnected || !inputChannel || inputChannel.readyState !== 'open') return;
    inputChannel.send(JSON.stringify({
      t: 'mu',
      b: e.button
    }));
  });

  canvas.addEventListener('wheel', (e) => {
    if (!isConnected || !inputChannel || inputChannel.readyState !== 'open') return;
    e.preventDefault();
    inputChannel.send(JSON.stringify({
      t: 'w',
      dy: e.deltaY > 0 ? -120 : 120
    }));
  }, { passive: false });
}

function setupKeyboardEvents() {
  window.addEventListener('keydown', (e) => {
    if (!isConnected || !inputChannel || inputChannel.readyState !== 'open') return;
    if (e.key === 'F12') return;

    e.preventDefault();
    inputChannel.send(JSON.stringify({
      t: 'kd',
      k: e.key,
      c: e.code,
      kc: e.keyCode
    }));
  });

  window.addEventListener('keyup', (e) => {
    if (!isConnected || !inputChannel || inputChannel.readyState !== 'open') return;
    if (e.key === 'F12') return;

    e.preventDefault();
    inputChannel.send(JSON.stringify({
      t: 'ku',
      k: e.key,
      c: e.code,
      kc: e.keyCode
    }));
  });
}

function sendSpecialKey(type) {
  if (!isConnected || !inputChannel || inputChannel.readyState !== 'open') return;

  if (type === 'ctrl_alt_del') {
    inputChannel.send(JSON.stringify({ t: 'kd', k: 'Control', c: 'ControlLeft', kc: 17 }));
    inputChannel.send(JSON.stringify({ t: 'kd', k: 'Alt', c: 'AltLeft', kc: 18 }));
    inputChannel.send(JSON.stringify({ t: 'kd', k: 'Delete', c: 'Delete', kc: 46 }));
    setTimeout(() => {
      inputChannel.send(JSON.stringify({ t: 'ku', k: 'Delete', c: 'Delete', kc: 46 }));
      inputChannel.send(JSON.stringify({ t: 'ku', k: 'Alt', c: 'AltLeft', kc: 18 }));
      inputChannel.send(JSON.stringify({ t: 'ku', k: 'Control', c: 'ControlLeft', kc: 17 }));
    }, 100);
  } else if (type === 'win_d') {
    inputChannel.send(JSON.stringify({ t: 'kd', k: 'Meta', c: 'MetaLeft', kc: 91 }));
    inputChannel.send(JSON.stringify({ t: 'kd', k: 'd', c: 'KeyD', kc: 68 }));
    setTimeout(() => {
      inputChannel.send(JSON.stringify({ t: 'ku', k: 'd', c: 'KeyD', kc: 68 }));
      inputChannel.send(JSON.stringify({ t: 'ku', k: 'Meta', c: 'MetaLeft', kc: 91 }));
    }, 100);
  } else if (type === 'alt_tab') {
    inputChannel.send(JSON.stringify({ t: 'kd', k: 'Alt', c: 'AltLeft', kc: 18 }));
    inputChannel.send(JSON.stringify({ t: 'kd', k: 'Tab', c: 'Tab', kc: 9 }));
    setTimeout(() => {
      inputChannel.send(JSON.stringify({ t: 'ku', k: 'Tab', c: 'Tab', kc: 9 }));
      inputChannel.send(JSON.stringify({ t: 'ku', k: 'Alt', c: 'AltLeft', kc: 18 }));
    }, 100);
  }
}

// Recent Connections Storage
function loadRecentConnections() {
  const raw = localStorage.getItem('ra_recent_connections');
  const list = raw ? JSON.parse(raw) : [];
  const container = document.getElementById('recent-list');

  if (list.length === 0) {
    container.innerHTML = '<span style="font-size: 0.85rem; color: var(--text-muted);">Nenhuma conexão recente ainda.</span>';
    return;
  }

  container.innerHTML = '';
  list.forEach((id) => {
    const chip = document.createElement('div');
    chip.className = 'recent-chip';
    chip.innerHTML = `💻 <span>${formatID(id)}</span>`;
    chip.onclick = () => {
      document.getElementById('target-id').value = formatID(id);
      document.getElementById('target-pwd').focus();
    };
    container.appendChild(chip);
  });
}

function saveRecentConnection(id) {
  let list = JSON.parse(localStorage.getItem('ra_recent_connections') || '[]');
  list = [id, ...list.filter((x) => x !== id)].slice(0, 5);
  localStorage.setItem('ra_recent_connections', JSON.stringify(list));
  loadRecentConnections();
}
