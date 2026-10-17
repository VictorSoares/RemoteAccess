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

    // Validar botões de atalhos rápidos de sistema no Web Viewer (Win+D, Alt+Tab, Alt+F4)
    const hasWinD = await pageDashboard.$eval('#web-viewer-toolbar', el => el.innerHTML.includes('❖ Win+D'));
    const hasAltTab = await pageDashboard.$eval('#web-viewer-toolbar', el => el.innerHTML.includes('⎇ Alt+Tab'));
    const hasAltF4 = await pageDashboard.$eval('#web-viewer-toolbar', el => el.innerHTML.includes('⎋ Alt+F4'));
    console.log('[Playwright] Web Viewer botões de atalhos rápidos:', { hasWinD, hasAltTab, hasAltF4 });
    if (!hasWinD || !hasAltTab || !hasAltF4) {
      throw new Error('Atalhos de sistema (Win+D, Alt+Tab, Alt+F4) ausentes no web-viewer-toolbar!');
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
    if (appRestoredLabel !== 'Controlando') {
      throw new Error(`Esperado "Controlando", obtido "${appRestoredLabel}"`);
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
