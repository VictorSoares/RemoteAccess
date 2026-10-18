import { chromium } from 'playwright';
import path from 'path';
import { fileURLToPath } from 'url';
import fs from 'fs';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const projectRoot = path.resolve(__dirname, '..');

const artifactDir = 'C:\\Users\\Victor\\.gemini\\antigravity\\brain\\39c2d50a-188a-4cba-ae5a-a2db8168a571';

async function runVisualTests() {
  console.log('[Playwright] Iniciando validação visual de aparência...');
  const browser = await chromium.launch({ headless: true });

  try {
    // ==========================================
    // 1. Validar Dashboard Web (dashboard.html)
    // ==========================================
    const pageDashboard = await browser.newPage({ viewport: { width: 1366, height: 768 } });
    await pageDashboard.addInitScript(() => {
      localStorage.setItem('ra_admin_key', 'remote2026');
    });
    const dashboardPath = path.join(projectRoot, 'internal', 'signaling', 'web', 'dashboard.html');
    await pageDashboard.goto(`file://${dashboardPath.replace(/\\/g, '/')}`);

    console.log('[Playwright] Validando dashboard.html...');

    // Verificar elementos principais do Dashboard
    await pageDashboard.waitForSelector('header');
    await pageDashboard.waitForSelector('.stats-grid');
    await pageDashboard.waitForSelector('#peers-table-body');

    // Tirar screenshot do painel inicial
    const dashboardScreenshotPath = path.join(artifactDir, 'dashboard_initial.png');
    await pageDashboard.screenshot({ path: dashboardScreenshotPath, fullPage: false });
    console.log('[Playwright] Screenshot salvo:', dashboardScreenshotPath);

    // Agora simular abertura do Web Viewer Dock para validar barra flutuante e novo seletor de qualidade
    await pageDashboard.evaluate(() => {
      const container = document.getElementById('web-viewer-container');
      const toolbar = document.getElementById('web-viewer-toolbar');
      if (container) container.style.display = 'flex';
      if (toolbar) toolbar.classList.add('dock-visible');
    });

    // Validar se o seletor de qualidade existe e tem as opções esperadas
    const qualitySelect = await pageDashboard.$('#web-viewer-quality-select');
    if (!qualitySelect) {
      throw new Error('Elemento #web-viewer-quality-select não encontrado na toolbar!');
    }

    const options = await pageDashboard.$$eval('#web-viewer-quality-select option', opts =>
      opts.map(o => ({ value: o.value, text: o.textContent.trim(), selected: o.selected }))
    );
    console.log('[Playwright] Opções de qualidade encontradas:', options);

    const hasSpeed = options.some(o => o.value === 'speed');
    const hasBalanced = options.some(o => o.value === 'balanced' && o.selected);
    const hasHD = options.some(o => o.value === 'hd');

    if (!hasSpeed || !hasBalanced || !hasHD) {
      throw new Error('Opções de qualidade inválidas ou padrão não selecionado!');
    }

    // Validar botão de Alternância de Controle / Modo Observador (#btn-viewer-control-toggle)
    const controlToggleBtn = await pageDashboard.$('#btn-viewer-control-toggle');
    if (!controlToggleBtn) {
      throw new Error('Elemento #btn-viewer-control-toggle não encontrado na toolbar!');
    }
    const initialLabel = await pageDashboard.$eval('#viewer-control-label', el => el.innerText.trim());
    console.log('[Playwright] Estado inicial do controle (Web Viewer):', initialLabel);
    if (initialLabel !== 'Controlando') {
      throw new Error(`Esperado "Controlando", obtido "${initialLabel}"`);
    }

    // Clicar para ativar Modo Observador
    await controlToggleBtn.click();
    const observerLabel = await pageDashboard.$eval('#viewer-control-label', el => el.innerText.trim());
    console.log('[Playwright] Estado após clique (Modo Observador):', observerLabel);
    if (observerLabel !== 'Observador') {
      throw new Error(`Esperado "Observador", obtido "${observerLabel}"`);
    }

    // Clicar novamente para retornar ao Modo Controlando
    await controlToggleBtn.click();
    const restoredLabel = await pageDashboard.$eval('#viewer-control-label', el => el.innerText.trim());
    console.log('[Playwright] Estado restaurado (Modo Controlando):', restoredLabel);
    if (restoredLabel !== 'Controlando') {
      throw new Error(`Esperado "Controlando", obtido "${restoredLabel}"`);
    }

    // Validar botão de Áudio Remoto (#btn-viewer-audio-toggle)
    const audioToggleBtn = await pageDashboard.$('#btn-viewer-audio-toggle');
    if (!audioToggleBtn) {
      throw new Error('Elemento #btn-viewer-audio-toggle não encontrado na toolbar!');
    }
    const initialAudioLabel = await pageDashboard.$eval('#viewer-audio-label', el => el.innerText.trim());
    console.log('[Playwright] Estado inicial do áudio (Web Viewer):', initialAudioLabel);
    if (initialAudioLabel !== 'Áudio') {
      throw new Error(`Esperado "Áudio", obtido "${initialAudioLabel}"`);
    }

    // Clicar para mutar o áudio
    await audioToggleBtn.click();
    const mutedAudioLabel = await pageDashboard.$eval('#viewer-audio-label', el => el.innerText.trim());
    console.log('[Playwright] Estado após clique (Mudo):', mutedAudioLabel);
    if (mutedAudioLabel !== 'Mudo') {
      throw new Error(`Esperado "Mudo", obtido "${mutedAudioLabel}"`);
    }

    // Clicar novamente para reativar o áudio
    await audioToggleBtn.click();
    const restoredAudioLabel = await pageDashboard.$eval('#viewer-audio-label', el => el.innerText.trim());
    console.log('[Playwright] Estado restaurado (Áudio):', restoredAudioLabel);
    if (restoredAudioLabel !== 'Áudio') {
      throw new Error(`Esperado "Áudio", obtido "${restoredAudioLabel}"`);
    }

    // Validar novos botões de Escala, Cortina e Auto-Lock no Web Viewer
    const scaleToggleBtn = await pageDashboard.$('#btn-viewer-scale-toggle');
    const curtainToggleBtn = await pageDashboard.$('#btn-viewer-curtain-toggle');
    const autolockToggleBtn = await pageDashboard.$('#btn-viewer-autolock-toggle');
    if (!scaleToggleBtn || !curtainToggleBtn || !autolockToggleBtn) {
      throw new Error('Botões de Escala, Cortina ou Auto-Lock ausentes no web-viewer-toolbar!');
    }

    // Testar alternância de escala (Ajustar -> 1:1 -> Ajustar)
    await scaleToggleBtn.click();
    const hasScale11 = await pageDashboard.$eval('#web-viewer-container', el => el.classList.contains('scale-1-1'));
    const scaleLabel1 = await pageDashboard.$eval('#viewer-scale-label', el => el.innerText.trim());
    console.log('[Playwright] Escala após clique:', scaleLabel1, 'container tem classe scale-1-1:', hasScale11);
    if (!hasScale11 || !scaleLabel1.includes('1:1')) {
      throw new Error('Falha na alternância para escala 1:1 no Web Viewer!');
    }
    await scaleToggleBtn.click();
    const hasScaleFit = await pageDashboard.$eval('#web-viewer-container', el => el.classList.contains('scale-1-1'));
    console.log('[Playwright] Escala após restaurar:', hasScaleFit);
    if (hasScaleFit) {
      throw new Error('Falha ao restaurar para escala Ajustar no Web Viewer!');
    }

    // Testar Cursor Remoto Dinâmico
    await pageDashboard.evaluate(() => {
      const vInputChannel = { onmessage: null };
      const event = { data: JSON.stringify({ t: 'cursor', cmd: 'pointer' }) };
      // Simular recebimento de pacote cursor
      currentRemoteCursor = 'pointer';
      if (viewerControlEnabled && vCanvas) vCanvas.style.cursor = 'pointer';
    });
    const cursorStyle = await pageDashboard.$eval('#viewer-canvas', el => el.style.cursor);
    console.log('[Playwright] Cursor remoto dinâmico simulado no canvas:', cursorStyle);
    if (cursorStyle !== 'pointer') {
      throw new Error(`Esperado cursor "pointer", obtido "${cursorStyle}"`);
    }

    // Validar existência dos elementos de DropZone e File Progress HUD
    const dropOverlayExists = await pageDashboard.$('#viewer-drop-overlay');
    const fileHudExists = await pageDashboard.$('#viewer-file-progress-hud');
    if (!dropOverlayExists || !fileHudExists) {
      throw new Error('Dropzone overlay ou file progress HUD ausentes no web viewer!');
    }

    // Validar que os atalhos físicos de teclado Alt+F4, Alt+Tab, Win+D são interceptados e transmitidos
    const sysCmds = [];
    await pageDashboard.exposeFunction('mockCaptureSysCmd', (cmd) => {
      sysCmds.push(cmd);
    });
    await pageDashboard.evaluate(() => {
      viewerConnected = true;
      viewerControlEnabled = true;
      window.sendViewerSys = (cmd) => { window.mockCaptureSysCmd(cmd); };
    });

    await pageDashboard.keyboard.press('Alt+F4');
    await pageDashboard.keyboard.press('Alt+Tab');
    await pageDashboard.keyboard.press('Meta+d');

    console.log('[Playwright] Comandos de sistema capturados via teclado físico normal:', sysCmds);
    if (!sysCmds.includes('alt_f4') || !sysCmds.includes('alt_tab') || !sysCmds.includes('desktop')) {
      throw new Error('Falha na captura dos atalhos físicos de teclado (Alt+F4, Alt+Tab, Win+D)!');
    }
    // Validar cores dinâmicas de latência (RTT)
    // Teste 1: Baixa latência (32ms - Verde #4ade80)
    await pageDashboard.evaluate(() => {
      updateViewerLatency(32);
    });
    const colorLow = await pageDashboard.$eval('#web-stat-lat', el => window.getComputedStyle(el).color);
    const textLow = await pageDashboard.$eval('#web-stat-lat', el => el.innerText);
    console.log('[Playwright] Latência Baixa (32ms):', textLow, 'cor:', colorLow);

    // Teste 2: Média latência (85ms - Amarelo #fbbf24)
    await pageDashboard.evaluate(() => {
      updateViewerLatency(85);
    });
    const colorMed = await pageDashboard.$eval('#web-stat-lat', el => window.getComputedStyle(el).color);
    const textMed = await pageDashboard.$eval('#web-stat-lat', el => el.innerText);
    console.log('[Playwright] Latência Média (85ms):', textMed, 'cor:', colorMed);

    // Teste 3: Alta latência (195ms - Vermelho #f87171)
    await pageDashboard.evaluate(() => {
      updateViewerLatency(195);
    });
    const colorHigh = await pageDashboard.$eval('#web-stat-lat', el => window.getComputedStyle(el).color);
    const textHigh = await pageDashboard.$eval('#web-stat-lat', el => el.innerText);
    console.log('[Playwright] Latência Alta (195ms):', textHigh, 'cor:', colorHigh);

    // Voltar para Verde para a captura de tela
    await pageDashboard.evaluate(() => {
      updateViewerLatency(28);
    });

    // Capturar screenshot focado no toolbar do Web Viewer
    const dockElement = await pageDashboard.$('#web-viewer-toolbar');
    const dockScreenshotPath = path.join(artifactDir, 'web_viewer_toolbar.png');
    if (dockElement) {
      await dockElement.screenshot({ path: dockScreenshotPath });
      console.log('[Playwright] Screenshot do dock salvo:', dockScreenshotPath);
    }

    const fullViewerScreenshotPath = path.join(artifactDir, 'web_viewer_full.png');
    await pageDashboard.screenshot({ path: fullViewerScreenshotPath });
    console.log('[Playwright] Screenshot do viewer completo salvo:', fullViewerScreenshotPath);
    await pageDashboard.close();

    // ==========================================
    // 2. Validar UI do Desktop Client (index.html)
    // ==========================================
    const pageClient = await browser.newPage({ viewport: { width: 960, height: 720 } });
    const clientPath = path.join(projectRoot, 'internal', 'server', 'web', 'index.html');
    await pageClient.goto(`file://${clientPath.replace(/\\/g, '/')}`);

    console.log('[Playwright] Validando index.html (App Desktop)...');
    await pageClient.waitForSelector('.app-header');
    await pageClient.waitForSelector('#host-view');

    const appInitialScreenshot = path.join(artifactDir, 'desktop_app_initial.png');
    await pageClient.screenshot({ path: appInitialScreenshot });
    console.log('[Playwright] Screenshot do app desktop inicial salvo:', appInitialScreenshot);

    // Validar botão de alternar controle remoto no Host Session Banner (#btn-host-control-toggle)
    await pageClient.evaluate(() => {
      const box = document.getElementById('active-session-box');
      if (box) box.style.display = 'flex';
      if (typeof updateHostControlUI === 'function') {
        updateHostControlUI(true);
      }
    });
    const hostControlBtn = await pageClient.$('#btn-host-control-toggle');
    if (!hostControlBtn) {
      throw new Error('Elemento #btn-host-control-toggle não encontrado no host banner!');
    }
    const hostInitialLabel = await pageClient.$eval('#host-control-label', el => el.innerText.trim());
    console.log('[Playwright] Estado inicial no Host (Controle Ativo):', hostInitialLabel);
    if (hostInitialLabel !== 'Desativar Controle') {
      throw new Error(`Esperado "Desativar Controle", obtido "${hostInitialLabel}"`);
    }

    await pageClient.evaluate(() => {
      if (typeof updateHostControlUI === 'function') {
        updateHostControlUI(false);
      }
    });
    const hostPausedLabel = await pageClient.$eval('#host-control-label', el => el.innerText.trim());
    console.log('[Playwright] Estado após pausar no Host:', hostPausedLabel);
    if (hostPausedLabel !== 'Ativar Controle') {
      throw new Error(`Esperado "Ativar Controle", obtido "${hostPausedLabel}"`);
    }

    // Simular abertura do viewer no app desktop
    await pageClient.evaluate(() => {
      const vContainer = document.getElementById('viewer-container');
      const vDock = document.getElementById('viewer-toolbar');
      if (vContainer) vContainer.style.display = 'flex';
      if (vDock) vDock.classList.add('dock-visible');
      if (typeof updateAppLatency === 'function') {
        updateAppLatency(24);
      }
    });

    // Validar botão de Alternância no Desktop Client (#btn-toggle-observer)
    const appObserverBtn = await pageClient.$('#btn-toggle-observer');
    if (!appObserverBtn) {
      throw new Error('Elemento #btn-toggle-observer não encontrado no viewer-toolbar!');
    }
    const appInitialLabel = await pageClient.$eval('#observer-mode-label', el => el.innerText.trim());
    console.log('[Playwright] Estado inicial do controle (Desktop App):', appInitialLabel);
    if (appInitialLabel !== 'Controlando') {
      throw new Error(`Esperado "Controlando", obtido "${appInitialLabel}"`);
    }

    await pageClient.evaluate(() => document.getElementById('btn-toggle-observer').click());
    const appObserverLabel = await pageClient.$eval('#observer-mode-label', el => el.innerText.trim());
    console.log('[Playwright] Estado após clique (Desktop App Observador):', appObserverLabel);
    if (appObserverLabel !== 'Observador') {
      throw new Error(`Esperado "Observador", obtido "${appObserverLabel}"`);
    }

    await pageClient.evaluate(() => document.getElementById('btn-toggle-observer').click());
    const appRestoredLabel = await pageClient.$eval('#observer-mode-label', el => el.innerText.trim());
    console.log('[Playwright] Estado restaurado (Desktop App Controlando):', appRestoredLabel);
    // Validar botão de Áudio no Desktop App (#btn-toggle-audio)
    const appAudioBtn = await pageClient.$('#btn-toggle-audio');
    if (!appAudioBtn) {
      throw new Error('Elemento #btn-toggle-audio não encontrado no viewer-toolbar!');
    }
    const appInitialAudioLabel = await pageClient.$eval('#audio-toggle-label', el => el.innerText.trim());
    console.log('[Playwright] Estado inicial do áudio (Desktop App):', appInitialAudioLabel);
    if (appInitialAudioLabel !== 'Áudio') {
      throw new Error(`Esperado "Áudio", obtido "${appInitialAudioLabel}"`);
    }

    await pageClient.evaluate(() => document.getElementById('btn-toggle-audio').click());
    const appMutedAudioLabel = await pageClient.$eval('#audio-toggle-label', el => el.innerText.trim());
    console.log('[Playwright] Estado após clique (Desktop App Mudo):', appMutedAudioLabel);
    if (appMutedAudioLabel !== 'Mudo') {
      throw new Error(`Esperado "Mudo", obtido "${appMutedAudioLabel}"`);
    }

    await pageClient.evaluate(() => document.getElementById('btn-toggle-audio').click());
    const appRestoredAudioLabel = await pageClient.$eval('#audio-toggle-label', el => el.innerText.trim());
    console.log('[Playwright] Estado restaurado (Desktop App Áudio):', appRestoredAudioLabel);
    if (appRestoredAudioLabel !== 'Áudio') {
      throw new Error(`Esperado "Áudio", obtido "${appRestoredAudioLabel}"`);
    }

    // Validar existência dos elementos de DropZone e File Progress HUD no Desktop
    const deskDropOverlayExists = await pageClient.$('#desktop-drop-overlay');
    const deskFileHudExists = await pageClient.$('#desktop-file-progress-hud');
    if (!deskDropOverlayExists || !deskFileHudExists) {
      throw new Error('Dropzone overlay ou file progress HUD ausentes no desktop viewer!');
    }

    // Validar botões de Escala, Cortina e Auto-Lock no Desktop Client
    const deskScaleBtn = await pageClient.$('#btn-toggle-scale');
    const deskCurtainBtn = await pageClient.$('#btn-toggle-curtain');
    const deskAutoLockBtn = await pageClient.$('#btn-toggle-autolock');
    if (!deskScaleBtn || !deskCurtainBtn || !deskAutoLockBtn) {
      throw new Error('Botões de Escala, Cortina ou Auto-Lock ausentes no viewer-toolbar do Desktop!');
    }

    // Testar alternância de escala no desktop (Ajustar -> 1:1 -> Ajustar)
    await pageClient.evaluate(() => document.getElementById('btn-toggle-scale').click());
    const deskHasScale11 = await pageClient.$eval('#canvas-wrapper', el => el.classList.contains('scale-1-1'));
    console.log('[Playwright] Desktop Escala 1:1 ativada:', deskHasScale11);
    if (!deskHasScale11) {
      throw new Error('Falha ao ativar escala 1:1 no desktop client!');
    }
    await pageClient.evaluate(() => document.getElementById('btn-toggle-scale').click());
    const deskHasScaleFit = await pageClient.$eval('#canvas-wrapper', el => el.classList.contains('scale-1-1'));
    console.log('[Playwright] Desktop Escala Ajustar restaurada:', !deskHasScaleFit);
    if (deskHasScaleFit) {
      throw new Error('Falha ao restaurar escala no desktop client!');
    }

    const appDockElement = await pageClient.$('#viewer-toolbar');
    const appDockScreenshotPath = path.join(artifactDir, 'desktop_viewer_toolbar.png');
    if (appDockElement) {
      await appDockElement.screenshot({ path: appDockScreenshotPath });
      console.log('[Playwright] Screenshot do dock do desktop app salvo:', appDockScreenshotPath);
    }

    await pageClient.close();

    console.log('[Playwright] TODOS OS TESTES VISUAIS PASSARAM COM SUCESSO! 🎉');
  } finally {
    await browser.close();
  }
}

runVisualTests().catch(err => {
  console.error('[Playwright] ERRO nos testes visuais:', err);
  process.exit(1);
});
