// Primitive desktop UI.
//
// Wails injects two globals into the page:
//   window.go.main.App.<Method>()  — the bound Go methods (return Promises)
//   window.runtime                 — EventsOn / EventsEmit / dialogs / etc.
//
// This template has no bundler, so there are no imports: everything below runs
// as a plain script.
(function () {
    'use strict';

    var els = {
        statusBadge: document.getElementById('statusBadge'),
        dropzone: document.getElementById('dropzone'),
        dropHint: document.getElementById('dropHint'),
        targetImg: document.getElementById('targetImg'),
        chooseBtn: document.getElementById('chooseBtn'),
        inputMeta: document.getElementById('inputMeta'),

        mode: document.getElementById('mode'),
        count: document.getElementById('count'),
        alpha: document.getElementById('alpha'),
        alphaOut: document.getElementById('alphaOut'),
        inputSize: document.getElementById('inputSize'),
        outputSize: document.getElementById('outputSize'),
        repeat: document.getElementById('repeat'),
        workers: document.getElementById('workers'),
        background: document.getElementById('background'),

        canvasImg: document.getElementById('canvasImg'),
        canvasEmpty: document.getElementById('canvasEmpty'),
        statFrame: document.getElementById('statFrame'),
        statScore: document.getElementById('statScore'),
        statNps: document.getElementById('statNps'),
        statElapsed: document.getElementById('statElapsed'),
        progressBar: document.getElementById('progressBar'),

        startBtn: document.getElementById('startBtn'),
        stopBtn: document.getElementById('stopBtn'),
        savePngBtn: document.getElementById('savePngBtn'),
        saveJpgBtn: document.getElementById('saveJpgBtn'),
        saveSvgBtn: document.getElementById('saveSvgBtn'),
        saveGifBtn: document.getElementById('saveGifBtn'),

        gifSize: document.getElementById('gifSize'),
        gifFrames: document.getElementById('gifFrames'),
        gifEngine: document.getElementById('gifEngine'),
        gifEngineHint: document.getElementById('gifEngineHint'),
        gifDither: document.getElementById('gifDither'),

        toast: document.getElementById('toast'),
    };

    var state = {
        running: false,
        hasInput: false,
        hasResult: false,
        toastTimer: null,
    };

    // ---------------------------------------------------------------- helpers

    function app() {
        return window.go && window.go.main && window.go.main.App;
    }

    // The bindings are injected during webview init. Poll briefly in case this
    // script wins the race.
    function whenReady(fn) {
        if (app()) {
            fn();
            return;
        }
        var tries = 0;
        var timer = setInterval(function () {
            if (app()) {
                clearInterval(timer);
                fn();
            } else if (++tries > 400) {
                clearInterval(timer);
                console.error('Wails bindings never became available');
            }
        }, 25);
    }

    function clampInt(value, lo, hi, fallback) {
        var n = parseInt(value, 10);
        if (isNaN(n)) return fallback;
        return Math.min(hi, Math.max(lo, n));
    }

    function errText(err) {
        if (!err) return 'Unknown error';
        if (typeof err === 'string') return err;
        return err.message || String(err);
    }

    function toast(message, kind) {
        els.toast.textContent = message;
        els.toast.className = 'toast' + (kind ? ' ' + kind : '');
        if (state.toastTimer) clearTimeout(state.toastTimer);
        state.toastTimer = setTimeout(function () {
            els.toast.classList.add('hidden');
        }, 6000);
    }

    function formatNps(nps) {
        if (!nps || nps <= 0) return '—';
        return nps >= 1000 ? (nps / 1000).toFixed(1) + 'k/s' : Math.round(nps) + '/s';
    }

    function formatElapsed(seconds) {
        if (!seconds || seconds <= 0) return '—';
        return seconds < 60 ? seconds.toFixed(1) + 's' : Math.floor(seconds / 60) + 'm ' + Math.round(seconds % 60) + 's';
    }

    // ------------------------------------------------------------ ui updates

    function setRunning(running) {
        state.running = running;
        els.statusBadge.textContent = running ? 'Running' : (state.hasResult ? 'Ready' : 'Idle');
        els.statusBadge.className = 'badge ' +
            (running ? 'badge-running' : (state.hasResult ? 'badge-done' : 'badge-idle'));
        els.startBtn.disabled = running || !state.hasInput;
        els.stopBtn.disabled = !running;
        updateSaveButtons();
    }

    function updateSaveButtons() {
        var enabled = state.hasResult && !state.running;
        els.savePngBtn.disabled = !enabled;
        els.saveJpgBtn.disabled = !enabled;
        els.saveSvgBtn.disabled = !enabled;
        els.saveGifBtn.disabled = !enabled;
    }

    function setInput(info) {
        state.hasInput = !!(info && info.path);
        if (!state.hasInput) {
            els.targetImg.classList.add('hidden');
            els.dropHint.classList.remove('hidden');
            els.inputMeta.textContent = 'No image selected';
            els.startBtn.disabled = true;
            return;
        }
        els.targetImg.src = info.preview;
        els.targetImg.classList.remove('hidden');
        els.dropHint.classList.add('hidden');
        els.inputMeta.textContent = info.name + ' · ' + info.width + '×' + info.height;
        els.startBtn.disabled = state.running;
    }

    function showResult(info) {
        if (info.preview) {
            els.canvasImg.src = info.preview;
            els.canvasImg.classList.remove('hidden');
            els.canvasEmpty.classList.add('hidden');
        }
        if (info.frame !== undefined) {
            els.statFrame.textContent = info.frame + ' / ' + info.total;
            els.progressBar.style.width = (info.total ? (info.frame / info.total) * 100 : 0) + '%';
        }
        if (info.score !== undefined) els.statScore.textContent = info.score.toFixed(6);
        if (info.nps !== undefined) els.statNps.textContent = formatNps(info.nps);
        if (info.elapsed !== undefined) els.statElapsed.textContent = formatElapsed(info.elapsed);
    }

    function readConfig() {
        return {
            inputPath: '',
            count: clampInt(els.count.value, 1, 5000, 100),
            mode: clampInt(els.mode.value, 0, 8, 1),
            alpha: clampInt(els.alpha.value, 0, 255, 128),
            repeat: clampInt(els.repeat.value, 0, 20, 0),
            inputSize: clampInt(els.inputSize.value, 16, 2048, 256),
            outputSize: clampInt(els.outputSize.value, 32, 4096, 1024),
            background: els.background.value.trim(),
            workers: clampInt(els.workers.value, 0, 32, 0),
        };
    }

    // --------------------------------------------------------------- actions

    function chooseImage() {
        app().PickInput()
            .then(function (info) {
                if (!info || !info.path) return; // dialog cancelled
                setInput(info);
                resetResult();
            })
            .catch(function (err) { toast(errText(err), 'error'); });
    }

    function resetResult() {
        state.hasResult = false;
        els.canvasImg.classList.add('hidden');
        els.canvasEmpty.classList.remove('hidden');
        els.statFrame.textContent = '—';
        els.statScore.textContent = '—';
        els.statNps.textContent = '—';
        els.statElapsed.textContent = '—';
        els.progressBar.style.width = '0%';
        setRunning(false);
    }

    function startRun() {
        var cfg = readConfig();
        els.progressBar.style.width = '0%';
        app().Start(cfg)
            .then(function () {
                state.hasResult = true;
                setRunning(true);
                els.canvasEmpty.classList.add('hidden');
                toast('Running — adding ' + cfg.count + ' shapes…');
            })
            .catch(function (err) { toast(errText(err), 'error'); });
    }

    function stopRun() {
        app().Stop();
        toast('Stopping after the current shape…');
    }

    function save(format) {
        app().Save(format)
            .then(function (path) {
                if (!path) return; // cancelled
                var parts = path.split('/');
                toast('Saved ' + parts[parts.length - 1], 'ok');
            })
            .catch(function (err) { toast(errText(err), 'error'); });
    }

    function saveGif() {
        var opts = {
            engine: els.gifEngine.value,
            maxSize: clampInt(els.gifSize.value, 64, 4096, 480),
            maxFrames: clampInt(els.gifFrames.value, 2, 2000, 100),
            dither: els.gifDither.checked,
        };
        toast('Encoding GIF\u2026');
        app().SaveGIF(opts)
            .then(function (path) {
                if (!path) return; // cancelled
                var parts = path.split('/');
                toast('Saved ' + parts[parts.length - 1], 'ok');
            })
            .catch(function (err) { toast(errText(err), 'error'); });
    }

    // Ask the backend which GIF encoders exist so the ImageMagick option is
    // only offered when it can actually be used.
    function loadGifEncoders() {
        app().GIFEncoders()
            .then(function (info) {
                var option = els.gifEngine.querySelector('option[value="imagemagick"]');
                if (info.imageMagick) {
                    if (option) option.disabled = false;
                    els.gifEngineHint.textContent = 'ImageMagick found at ' + info.imageMagickPath + '.';
                } else {
                    if (option) option.disabled = true;
                    // Never leave the select on a disabled option.
                    els.gifEngine.value = 'go';
                    els.gifEngineHint.textContent =
                        'ImageMagick is not installed \u2014 the built-in encoder is used.';
                }
            })
            .catch(function () {
                els.gifEngineHint.textContent = 'Could not check for ImageMagick.';
            });
    }

    // ---------------------------------------------------------------- events

    function wireEvents() {
        if (!window.runtime || !window.runtime.EventsOn) return;

        window.runtime.EventsOn('run:progress', function (p) {
            state.hasResult = true;
            if (state.running === false) setRunning(true);
            showResult(p);
        });

        window.runtime.EventsOn('run:done', function (d) {
            setRunning(false);
            state.hasResult = true;
            if (d && d.cancelled) {
                toast('Stopped after ' + d.frame + ' shapes — score ' + Number(d.score).toFixed(6));
            } else if (d) {
                els.progressBar.style.width = '100%';
                toast('Finished ' + d.frame + ' shapes — score ' + Number(d.score).toFixed(6), 'ok');
            }
        });

        window.runtime.EventsOn('input:changed', function (info) {
            setInput(info);
            resetResult();
            toast('Loaded ' + info.name, 'ok');
        });

        window.runtime.EventsOn('input:error', function (message) {
            toast(message, 'error');
        });
    }

    function wireControls() {
        els.chooseBtn.addEventListener('click', chooseImage);
        els.dropzone.addEventListener('click', chooseImage);
        els.startBtn.addEventListener('click', startRun);
        els.stopBtn.addEventListener('click', stopRun);
        els.savePngBtn.addEventListener('click', function () { save('png'); });
        els.saveJpgBtn.addEventListener('click', function () { save('jpg'); });
        els.saveSvgBtn.addEventListener('click', function () { save('svg'); });
        els.saveGifBtn.addEventListener('click', saveGif);

        els.alpha.addEventListener('input', function () {
            els.alphaOut.textContent = els.alpha.value;
        });

        // Highlight the drop zone while a file is dragged over the window.
        ['dragenter', 'dragover'].forEach(function (name) {
            els.dropzone.addEventListener(name, function (e) {
                e.preventDefault();
                els.dropzone.classList.add('drop-active');
            });
        });
        ['dragleave', 'drop'].forEach(function (name) {
            els.dropzone.addEventListener(name, function () {
                els.dropzone.classList.remove('drop-active');
            });
        });

        // Enter in a number/text field starts the run.
        [els.count, els.background].forEach(function (el) {
            el.addEventListener('keydown', function (e) {
                if (e.key === 'Enter' && !state.running && state.hasInput) startRun();
            });
        });

        // Cmd+Enter starts, Escape stops.
        document.addEventListener('keydown', function (e) {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                if (!state.running && state.hasInput) startRun();
            } else if (e.key === 'Escape' && state.running) {
                stopRun();
            }
        });
    }

    // ------------------------------------------------------------------ boot

    whenReady(function () {
        wireControls();
        wireEvents();
        setRunning(false);
        loadGifEncoders();

        app().Status()
            .then(function (s) {
                if (s.input && s.input.path) setInput(s.input);
                // Record the result before refreshing the controls, so that
                // setRunning() enables the save buttons.
                state.hasResult = !!s.hasResult;
                if (state.hasResult) {
                    showResult({ frame: s.frame, total: s.total, score: s.score });
                }
                setRunning(!!s.running);
            })
            .catch(function (err) { toast(errText(err), 'error'); });
    });
})();
