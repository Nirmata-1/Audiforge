document.addEventListener('DOMContentLoaded', () => {
    const dropZone = document.getElementById('drop-zone');
    const fileInput = document.getElementById('file-input');
    const processingDiv = document.getElementById('processing');
    const completeDiv = document.getElementById('complete');
    const subtitle = completeDiv.querySelector('.subtitle');
    let currentConversionId = null;
  
    // Drag & drop handlers
    const handleDrag = (e) => {
        e.preventDefault();
        dropZone.style.borderColor = 'var(--brand-purple)';
        dropZone.style.background = 'rgba(255, 255, 255, 0.8)';
    };
  
    dropZone.addEventListener('dragover', handleDrag);
    dropZone.addEventListener('dragleave', () => {
        dropZone.style.borderColor = 'var(--brand-mid)';
        dropZone.style.background = 'rgba(255, 255, 255, 0.95)';
    });
  
    dropZone.addEventListener('drop', (e) => {
        e.preventDefault();
        handleFile(e.dataTransfer.files[0]);
        resetDropZone();
    });
  
    // Single click handler
    dropZone.addEventListener('click', () => fileInput.click());
  
    // File input handler
    fileInput.addEventListener('change', (e) => {
        if (e.target.files.length > 0) {
            handleFile(e.target.files[0]);
            e.target.value = '';
        }
    });
  
    // Download handler
    document.getElementById('download-again').addEventListener('click', () => {
        if (currentConversionId) {
            window.location.href = `/download/${currentConversionId}`;
        }
    });
  
    // New conversion handler
    document.getElementById('new-conversion').addEventListener('click', resetUI);
  
    async function handleFile(file) {
        if (!file?.name.endsWith('.pdf')) return;
  
        const formData = new FormData();
        formData.append('file', file);
  
        try {
            showProcessing();
            const response = await fetch('/upload', {
                method: 'POST',
                body: formData
            });
            
            if (!response.ok) throw new Error(await response.text());
            
            const { id } = await response.json();
            currentConversionId = id;
            pollStatus(id);
        } catch (error) {
            showError(error.message);
        }
    }
  
    async function pollStatus(id) {
        try {
            const response = await fetch(`/status/${id}`);
            if (!response.ok) throw new Error('Status check failed');
            
            const status = await response.json();
            
            switch (status.status) {
                case 'completed':
                    showConversionComplete(status.movementCount);
                    break;
                case 'error':
                    throw new Error(status.message);
                default:
                    setTimeout(() => pollStatus(id), 1000);
            }
        } catch (error) {
            showError(error.message);
        }
    }
  
    function showProcessing() {
        dropZone.classList.add('hidden');
        processingDiv.classList.remove('hidden');
        completeDiv.classList.add('hidden');
    }
  
    function showConversionComplete(movementCount) {
        subtitle.textContent = `Successfully converted ${movementCount} ${movementCount === 1 ? 'movement' : 'movements'}`;
        processingDiv.classList.add('hidden');
        completeDiv.classList.remove('hidden');
    }
  
    function showError(message) {
        processingDiv.classList.add('hidden');
        completeDiv.innerHTML = `
            <h2 class="thank-you">⚠️ Conversion Error</h2>
            <p class="subtitle">${message}</p>
            <div class="button-group">
                <button class="btn reset-btn" onclick="location.reload()">
                    Try Again
                </button>
            </div>
        `;
        completeDiv.classList.remove('hidden');
    }
  
    function resetDropZone() {
        dropZone.style.borderColor = 'var(--brand-mid)';
        dropZone.style.background = 'rgba(255, 255, 255, 0.95)';
    }
  
    function resetUI() {
        currentConversionId = null;
        completeDiv.classList.add('hidden');
        dropZone.classList.remove('hidden');
        processingDiv.classList.add('hidden');
    }
  });