// Applies the saved color theme before first paint (dark unless the user picked light)
// and adds a toggle button to every page.
(function () {
    const storageKey = 'claude-review-theme';
    const root = document.documentElement;

    function savedTheme() {
        try {
            return localStorage.getItem(storageKey) === 'light' ? 'light' : 'dark';
        } catch {
            return 'dark';
        }
    }

    function applyTheme(theme) {
        root.dataset.theme = theme;
    }

    function updateButton() {
        const button = document.querySelector('.theme-toggle');
        if (!button) return;
        const dark = root.dataset.theme === 'dark';
        button.textContent = dark ? '☀' : '☾';
        button.title = dark ? 'Switch to light mode' : 'Switch to dark mode';
        button.setAttribute('aria-label', button.title);
    }

    applyTheme(savedTheme());

    document.addEventListener('DOMContentLoaded', () => {
        const button = document.createElement('button');
        button.type = 'button';
        button.className = 'theme-toggle';

        button.addEventListener('click', () => {
            const theme = root.dataset.theme === 'dark' ? 'light' : 'dark';
            applyTheme(theme);
            try {
                localStorage.setItem(storageKey, theme);
            } catch {
                // Storage unavailable; the choice lasts for this page only
            }
            updateButton();
        });

        document.body.appendChild(button);
        updateButton();
    });

    // Keep other open tabs in sync
    window.addEventListener('storage', (e) => {
        if (e.key === storageKey) {
            applyTheme(savedTheme());
            updateButton();
        }
    });
})();
