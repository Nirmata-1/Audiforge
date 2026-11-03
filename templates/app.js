document.addEventListener('DOMContentLoaded', () => {
    const dropZone = document.getElementById('drop-zone');
    const fileInput = document.getElementById('file-input');
    const fileInfo = document.getElementById('file-info');
    const processingDiv = document.getElementById('processing');
    const processingText = document.getElementById('processing-text');
    const stageText = document.getElementById('stage-text');
    const progressFill = document.getElementById('progress-fill');
    const progressText = document.getElementById('progress-text');
    const elapsedText = document.getElementById('elapsed-text');
    const completeDiv = document.getElementById('complete');
    const resultSubtitle = document.getElementById('result-subtitle');
    const resultWarning = document.getElementById('result-warning');
    const resultList = document.getElementById('result-list');
    const logLinkWrap = document.getElementById('log-link-wrap');
    const logLink = document.getElementById('log-link');
    const errorBox = document.getElementById('error-box');
    const errorText = document.getElementById('error-text');
    const errorLogDetails = document.getElementById('error-log-details');
    const errorLog = document.getElementById('error-log');
    let currentConversionId = null;
    let pollTimer = null;
    let elapsedTimer = null;
    let startedAt = 0;

    let shownProgress = 0;
    const setProgress = (pct, label) => {
        // Never go backwards, and never show 100% until the server
        // says the job is actually complete.
        shownProgress = Math.max(shownProgress, Math.max(0, Math.min(99, pct)));
        progressFill.style.width = shownProgress + '%';
        progressText.textContent = label || '';
    };
    const setProgressDone = () => {
        shownProgress = 100;
        progressFill.style.width = '100%';
    };
    const startElapsed = () => {
        startedAt = Date.now();
        clearInterval(elapsedTimer);
        elapsedTimer = setInterval(() => {
            const s = Math.floor((Date.now() - startedAt) / 1000);
            elapsedText.textContent = `Elapsed: ${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')} — Audiveris can take a few minutes on a dense page.`;
        }, 1000);
    };
    const stopTimers = () => { clearTimeout(pollTimer); clearInterval(elapsedTimer); };

    dropZone.addEventListener('dragover', (e) => { e.preventDefault(); dropZone.classList.add('drag-over'); });
    dropZone.addEventListener('dragleave', () => dropZone.classList.remove('drag-over'));
    dropZone.addEventListener('drop', (e) => {
        e.preventDefault();
        dropZone.classList.remove('drag-over');
        if (e.dataTransfer.files.length) handleFile(e.dataTransfer.files[0]);
    });
    dropZone.addEventListener('click', () => fileInput.click());
    dropZone.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); fileInput.click(); }
    });
    fileInput.addEventListener('change', (e) => {
        if (e.target.files.length) handleFile(e.target.files[0]);
        e.target.value = '';
    });
    document.getElementById('download-again').addEventListener('click', () => {
        if (currentConversionId) window.location.href = `/download/${currentConversionId}`;
    });
    document.getElementById('new-conversion').addEventListener('click', resetUI);
    document.getElementById('try-again').addEventListener('click', resetUI);

    function handleFile(file) {
        if (!file) return;
        if (!file.name.toLowerCase().endsWith('.pdf')) {
            showError('Only PDF files are accepted.');
            return;
        }
        if (file.size > 50 * 1024 * 1024) {
            showError('That PDF is over the 50 MB limit.');
            return;
        }
        fileInfo.textContent = `${file.name} (${(file.size / 1024).toFixed(0)} KB)`;
        showProcessing();
        startElapsed();
        // XHR (not fetch) so the upload itself can show progress;
        // the conversion after upload is the long part, and polling
        // reports its server-side message.
        const formData = new FormData();
        formData.append('file', file);
        formData.append('ocr_lang', document.getElementById('ocr-lang').value);
        const sheets = document.getElementById('sheets').value.trim();
        if (sheets && !/^\d+(-\d+)?(,\s*\d+(-\d+)?)*$/.test(sheets)) {
            showError('Pages must look like 1, 3-5, or be blank for all pages.');
            return;
        }
        formData.append('sheets', sheets.replace(/\s+/g, ''));
        if (document.getElementById('include-musicxml').checked) formData.append('include_musicxml', 'on');
        if (document.getElementById('include-omr').checked) formData.append('include_omr', 'on');
        if (document.getElementById('auto-download').checked) formData.append('auto_download', 'on');
        const xhr = new XMLHttpRequest();
        xhr.open('POST', '/upload');
        xhr.upload.onprogress = (e) => {
            if (e.lengthComputable) setProgress((e.loaded / e.total) * 90, `Uploading… ${Math.round((e.loaded / e.total) * 100)}%`);
        };
        xhr.onload = () => {
            if (xhr.status < 200 || xhr.status >= 300) { showError(xhr.responseText || 'Upload failed.'); return; }
            try {
                const { id } = JSON.parse(xhr.responseText);
                currentConversionId = id;
                setProgress(95, 'Uploaded. Converting…');
                pollStatus(id);
            } catch { showError('Unexpected server response.'); }
        };
        xhr.onerror = () => showError('Upload failed. Check your connection and try again.');
        xhr.send(formData);
    }

    async function pollStatus(id) {
        try {
            const response = await fetch(`/status/${id}`);
            if (!response.ok) throw new Error('Status check failed');
            const status = await response.json();
            if (status.message) processingText.textContent = status.message;
            if (status.stage) stageText.textContent = status.stage;
            if (status.status === 'completed') { setProgressDone(); progressText.textContent = 'Done'; showConversionComplete(status); return; }
            // Real progress when Audiveris reports a step; otherwise
            // creep toward 95% so the bar never stalls for minutes
            // on a dense page. It only reaches 100% on completion.
            const serverPct = status.progress || 0;
            const creep = Math.min(95, shownProgress + 2);
            setProgress(Math.max(serverPct, creep), serverPct ? `${serverPct}% — ${status.stage || ''}` : 'Converting…');
            if (status.status === 'error') throw Object.assign(new Error(status.message || 'Conversion failed.'), { logTail: status.logTail });
            pollTimer = setTimeout(() => pollStatus(id), 2000);
        } catch (error) {
            showError(error.message || 'Conversion failed.', error.logTail);
        }
    }

    function showProcessing() {
        shownProgress = 0;
        stageText.textContent = '';
        dropZone.classList.add('hidden');
        completeDiv.classList.add('hidden');
        errorBox.classList.add('hidden');
        processingDiv.classList.remove('hidden');
        processingText.textContent = 'Transforming your sheet music...';
        setProgress(5, 'Starting upload…');
    }

    function showConversionComplete(status) {
        stopTimers();
        const n = status.movementCount || 0;
        resultSubtitle.textContent = `Successfully converted ${n} ${n === 1 ? 'movement' : 'movements'}`;
        if (status.warning) {
            resultWarning.textContent = status.warning;
            resultWarning.classList.remove('hidden');
        } else resultWarning.classList.add('hidden');
        if (Array.isArray(status.files) && status.files.length) {
            resultList.innerHTML = '';
            status.files.forEach((f) => {
                const li = document.createElement('li');
                const parts = f.parts && f.parts.length ? ` — ${f.parts.join(', ')}` : '';
                const counts = (f.notes || f.measures) ? ` (${f.notes || 0} notes, ${f.measures || 0} measures${parts})` : parts;
                li.textContent = `${f.name}${counts}`;
                resultList.appendChild(li);
            });
            resultList.classList.remove('hidden');
        } else resultList.classList.add('hidden');
        if (currentConversionId) {
            logLink.href = `/log/${currentConversionId}`;
            logLinkWrap.classList.remove('hidden');
            if (status.options && status.options.autoDownload) {
                window.location.href = `/download/${currentConversionId}`;
            }
        }
        processingDiv.classList.add('hidden');
        completeDiv.classList.remove('hidden');
    }

    function showError(message, logTail) {
        stopTimers();
        processingDiv.classList.add('hidden');
        completeDiv.classList.add('hidden');
        errorText.textContent = message;
        if (logTail) { errorLog.textContent = logTail; errorLogDetails.classList.remove('hidden'); }
        else errorLogDetails.classList.add('hidden');
        errorBox.classList.remove('hidden');
    }

    function resetUI() {
        stopTimers();
        shownProgress = 0;
        stageText.textContent = '';
        currentConversionId = null;
        fileInfo.textContent = 'PDF only, up to 50 MB. One score per upload.';
        completeDiv.classList.add('hidden');
        processingDiv.classList.add('hidden');
        errorBox.classList.add('hidden');
        dropZone.classList.remove('hidden');
        setProgress(0, '');
        elapsedText.textContent = '';
    }
});
