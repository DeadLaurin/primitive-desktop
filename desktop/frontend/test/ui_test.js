// Headless test for frontend/src/main.js.
//
// Runs the real UI script against a minimal stub DOM and a stub Wails bridge,
// then fires the same events the Go backend emits. This catches wiring and
// formatting regressions without launching the webview.
//
//   node frontend/test/ui_test.js
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');
const assert = require('assert');

const MAIN_JS = path.join(__dirname, '..', 'src', 'main.js');

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

// Objects created inside the vm sandbox belong to a different realm, so their
// prototypes differ from this file's. Round-trip through JSON before comparing
// with deepStrictEqual.
const plain = (value) => JSON.parse(JSON.stringify(value));

// ---------------------------------------------------------------------------
// A stub DOM + Wails bridge, then the real main.js executed against it.
// ---------------------------------------------------------------------------
function createSandbox(config) {
    config = config || {};

    const elements = new Map();
    const queried = new Map();

    function makeElement(id) {
        const listeners = {};
        const classes = new Set();
        const el = {
            id,
            textContent: '',
            value: '',
            src: '',
            disabled: false,
            checked: false,
            className: '',
            style: {},
            classList: {
                add: (c) => classes.add(c),
                remove: (c) => classes.delete(c),
                contains: (c) => classes.has(c),
                _set: classes,
            },
            addEventListener: (name, fn) => {
                (listeners[name] = listeners[name] || []).push(fn);
            },
            // main.js uses this to find the ImageMagick <option>.
            querySelector: (sel) => {
                if (!queried.has(sel)) queried.set(sel, makeElement(sel));
                return queried.get(sel);
            },
            _fire: (name, ev) => (listeners[name] || []).forEach((fn) => fn(ev || {})),
            _classes: classes,
        };
        return el;
    }

    const document = {
        getElementById(id) {
            if (!elements.has(id)) elements.set(id, makeElement(id));
            return elements.get(id);
        },
        addEventListener: () => {},
    };

    const handlers = {};
    const calls = [];

    const App = {
        PickInput: () => Promise.resolve({ path: '', name: '', preview: '' }),
        LoadInput: () => Promise.resolve({}),
        Start: (cfg) => { calls.push(['Start', cfg]); return Promise.resolve(); },
        Stop: () => { calls.push(['Stop']); return Promise.resolve(); },
        Save: (fmt) => { calls.push(['Save', fmt]); return Promise.resolve(config.saveResult || ''); },
        SaveGIF: (opts) => { calls.push(['SaveGIF', opts]); return Promise.resolve(config.gifResult || ''); },
        GIFEncoders: () => Promise.resolve(config.gifEncoders || { builtIn: true, imageMagick: false, imageMagickPath: '' }),
        Status: () => Promise.resolve(config.status || { running: false, hasResult: false, input: {} }),
    };

    const window = {
        go: { main: { App } },
        runtime: {
            EventsOn: (name, cb) => { handlers[name] = cb; },
            EventsEmit: () => {},
        },
    };

    const sandbox = {
        window, document, console,
        setInterval, clearInterval, setTimeout, clearTimeout,
        parseInt, isNaN, Math,
    };

    vm.createContext(sandbox);
    vm.runInContext(fs.readFileSync(MAIN_JS, 'utf8'), sandbox, { filename: MAIN_JS });

    return {
        el: (id) => document.getElementById(id),
        queried: (id, sel) => document.getElementById(id).querySelector(sel),
        handlers,
        calls,
    };
}

async function main() {
    // =======================================================================
    // Scenario 1: ImageMagick is not installed (the common case).
    // =======================================================================
    const s = createSandbox({
        gifEncoders: { builtIn: true, imageMagick: false, imageMagickPath: '' },
    });
    const el = s.el;

    assert.ok(s.handlers['run:progress'], 'run:progress listener not registered');
    assert.ok(s.handlers['run:done'], 'run:done listener not registered');
    assert.ok(s.handlers['input:changed'], 'input:changed listener not registered');

    // --- loading a target enables Start and shows the thumbnail ---
    s.handlers['input:changed']({
        path: '/tmp/poster.jpg',
        name: 'poster.jpg',
        width: 290,
        height: 435,
        preview: 'data:image/png;base64,TARGET',
    });
    assert.strictEqual(el('targetImg').src, 'data:image/png;base64,TARGET', 'target thumbnail src');
    assert.strictEqual(el('inputMeta').textContent, 'poster.jpg · 290×435', 'input metadata');
    assert.strictEqual(el('startBtn').disabled, false, 'start enabled once an image is loaded');

    // --- a progress event should populate every stat, including speed ---
    s.handlers['run:progress']({
        runId: 1, frame: 42, total: 100, score: 0.123456, nps: 1234,
        elapsed: 3.2, preview: 'data:image/png;base64,AAAA',
    });
    assert.strictEqual(el('statFrame').textContent, '42 / 100', 'frame stat');
    assert.strictEqual(el('statScore').textContent, '0.123456', 'score stat');
    assert.strictEqual(el('statNps').textContent, '1.2k/s', 'speed stat must be populated');
    assert.strictEqual(el('statElapsed').textContent, '3.2s', 'elapsed stat');
    assert.strictEqual(el('progressBar').style.width, '42%', 'progress bar width');
    assert.strictEqual(el('canvasImg').src, 'data:image/png;base64,AAAA', 'preview src');
    assert.ok(el('canvasImg')._classes.has('hidden') === false, 'canvas should be visible');
    assert.strictEqual(el('statusBadge').textContent, 'Running', 'badge while running');
    assert.strictEqual(el('startBtn').disabled, true, 'start disabled while running');
    assert.strictEqual(el('stopBtn').disabled, false, 'stop enabled while running');

    // --- rate formatting ---
    s.handlers['run:progress']({ runId: 1, frame: 43, total: 100, score: 0.12, nps: 850, elapsed: 3.3 });
    assert.strictEqual(el('statNps').textContent, '850/s', 'sub-1000 rate formatting');

    s.handlers['run:progress']({ runId: 1, frame: 44, total: 100, score: 0.12, nps: 0, elapsed: 3.4 });
    assert.strictEqual(el('statNps').textContent, '—', 'zero rate renders as dash');

    // --- a progress event without a preview must not clear the canvas ---
    s.handlers['run:progress']({ runId: 1, frame: 45, total: 100, score: 0.11, nps: 900, elapsed: 3.5 });
    assert.strictEqual(el('canvasImg').src, 'data:image/png;base64,AAAA', 'preview preserved when omitted');

    // --- completion re-enables the controls ---
    s.handlers['run:done']({ runId: 1, cancelled: false, frame: 100, score: 0.05 });
    assert.strictEqual(el('statusBadge').textContent, 'Ready', 'badge after finish');
    assert.strictEqual(el('startBtn').disabled, false, 'start re-enabled');
    assert.strictEqual(el('stopBtn').disabled, true, 'stop disabled');
    assert.strictEqual(el('savePngBtn').disabled, false, 'save png enabled after a result');
    assert.strictEqual(el('saveGifBtn').disabled, false, 'save gif enabled after a result');
    assert.strictEqual(el('progressBar').style.width, '100%', 'progress complete');

    // --- a cancelled run reports Ready too ---
    s.handlers['run:progress']({ runId: 2, frame: 10, total: 100, score: 0.2, nps: 100, elapsed: 1 });
    s.handlers['run:done']({ runId: 2, cancelled: true, frame: 10, score: 0.2 });
    assert.strictEqual(el('statusBadge').textContent, 'Ready', 'badge after cancel');
    assert.ok(el('toast').textContent.indexOf('Stopped') === 0, 'cancel toast text');

    // --- Start forwards a clamped config ---
    el('count').value = '99999';
    el('mode').value = '3';
    el('alpha').value = '200';
    el('inputSize').value = '128';
    el('outputSize').value = '512';
    el('repeat').value = '-5';
    el('workers').value = '0';
    el('background').value = '  1a2b3c  ';
    el('startBtn')._fire('click');

    // --- Save buttons forward their format ---
    el('saveSvgBtn')._fire('click');

    // --- Save GIF forwards the chosen options ---
    el('gifSize').value = '720';
    el('gifFrames').value = '160';
    el('gifEngine').value = 'go';
    el('gifDither').checked = true;
    el('saveGifBtn')._fire('click');

    await tick();
    await tick();

    const start = s.calls.find((c) => c[0] === 'Start');
    assert.ok(start, 'Start was not called');
    const cfg = start[1];
    assert.strictEqual(cfg.count, 5000, 'count clamped to max 5000');
    assert.strictEqual(cfg.mode, 3, 'mode passed through');
    assert.strictEqual(cfg.alpha, 200, 'alpha passed through');
    assert.strictEqual(cfg.inputSize, 128, 'input size passed through');
    assert.strictEqual(cfg.outputSize, 512, 'output size passed through');
    assert.strictEqual(cfg.repeat, 0, 'negative repeat clamped to 0');
    assert.strictEqual(cfg.workers, 0, 'workers 0 means auto');
    assert.strictEqual(cfg.background, '1a2b3c', 'background trimmed');

    assert.ok(s.calls.some((c) => c[0] === 'Save' && c[1] === 'svg'), 'Save("svg") not called');

    const gif = s.calls.find((c) => c[0] === 'SaveGIF');
    assert.ok(gif, 'SaveGIF was not called');
    assert.deepStrictEqual(plain(gif[1]), {
        engine: 'go', maxSize: 720, maxFrames: 160, dither: true,
    }, 'GIF export options');

    // --- with no ImageMagick, the option is disabled and Go is selected ---
    await tick();
    assert.strictEqual(
        el('gifEngine').querySelector('option[value="imagemagick"]').disabled, true,
        'ImageMagick option should be disabled when it is not installed');
    assert.strictEqual(el('gifEngine').value, 'go', 'engine forced to the built-in encoder');
    assert.ok(
        el('gifEngineHint').textContent.indexOf('not installed') !== -1,
        'hint should say ImageMagick is missing, got: ' + el('gifEngineHint').textContent);

    // =======================================================================
    // Scenario 2: ImageMagick is installed.
    // =======================================================================
    const s2 = createSandbox({
        gifEncoders: { builtIn: true, imageMagick: true, imageMagickPath: '/opt/homebrew/bin/magick' },
    });
    await tick();
    await tick();

    assert.strictEqual(
        s2.el('gifEngine').querySelector('option[value="imagemagick"]').disabled, false,
        'ImageMagick option should be enabled when it is installed');
    assert.ok(
        s2.el('gifEngineHint').textContent.indexOf('/opt/homebrew/bin/magick') !== -1,
        'hint should name the ImageMagick path, got: ' + s2.el('gifEngineHint').textContent);

    // Choosing ImageMagick must reach the backend.
    s2.el('gifEngine').value = 'imagemagick';
    s2.el('gifSize').value = '320';
    s2.el('gifFrames').value = '60';
    s2.el('saveGifBtn')._fire('click');
    await tick();

    const gif2 = s2.calls.find((c) => c[0] === 'SaveGIF');
    assert.ok(gif2, 'SaveGIF was not called in scenario 2');
    assert.deepStrictEqual(plain(gif2[1]), {
        engine: 'imagemagick', maxSize: 320, maxFrames: 60, dither: false,
    }, 'GIF export options with ImageMagick');

    // =======================================================================
    // Scenario 3: a previous result means Status() re-enables the save buttons.
    // =======================================================================
    const s3 = createSandbox({
        status: {
            running: false, hasResult: true, frame: 50, total: 50, score: 0.03,
            input: { path: '/tmp/x.png', name: 'x.png', width: 10, height: 10, preview: 'data:,x' },
        },
    });
    await tick();
    await tick();
    assert.strictEqual(s3.el('startBtn').disabled, false, 'start enabled with a restored input');
    assert.strictEqual(s3.el('saveGifBtn').disabled, false, 'save gif enabled with a restored result');
    assert.strictEqual(s3.el('statScore').textContent, '0.030000', 'restored score stat');

    console.log('ui_test: all assertions passed');
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
