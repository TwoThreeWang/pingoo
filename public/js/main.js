const {body} = document, themes = ['light','dark'],
      toggle = id => document.getElementById(id);
    let currentTheme = localStorage.theme || 'light';

const updateTheme = theme => {
  const isDark = theme === 'dark';
  body.classList.toggle('dark_mode', isDark);
  if(toggle('theme-toggle')) toggle('theme-toggle').textContent = `☀︎ ${theme}`;
};

updateTheme(currentTheme);

toggle('theme-toggle')?.addEventListener('click', () => {
  currentTheme = themes[(themes.indexOf(currentTheme) + 1) % themes.length];
  updateTheme(currentTheme);
  localStorage.theme = currentTheme;
});

addEventListener('DOMContentLoaded', () => {
  const topBtn = toggle('top');
  if (topBtn) addEventListener('scroll', () => topBtn.classList.toggle('show', scrollY > 200));

  window.showMsg = (msg, type = 'info') => {
    const el = Object.assign(document.createElement('div'), {className: `msg ${type}`, textContent: msg});
    document.body.append(el);
    requestAnimationFrame(() => el.classList.add('show'));
    setTimeout(() => el.classList.remove('show'), 3000);
  };
});

const lightbox = document.getElementById('lightbox');
const lightboxImg = document.getElementById('lightboxImg');

const openLightbox = img => {
    lightbox.style.display = 'block';
    lightboxImg.src = img.src;
    document.body.style.overflow = 'hidden';
};

const closeLightbox = () => {
    lightbox.style.display = 'none';
    document.body.style.overflow = 'auto';
};
function showMessage(message, type) {
    const msg = document.createElement('div');
    msg.className = `msg ${type}`;
    msg.textContent = message;
    document.body.appendChild(msg);

    setTimeout(() => msg.classList.add('show'), 100);
    setTimeout(() => {
        msg.classList.remove('show');
        setTimeout(() => document.body.removeChild(msg), 500);
    }, 3000);
};
function toggleDiv(id){
  const el = document.getElementById(id);
  if(el) el.style.display = el.style.display === 'none' ? '' : 'none';
};

async function sendRequest(method, url, data, options = {}) {
    const token = localStorage.getItem('token');
    const headers = {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`
    };
    if (options.headers) Object.assign(headers, options.headers);
    const fetchOptions = {
        method,
        headers,
        credentials: 'include',
        ...options
    };
    if (data) {
        fetchOptions.body = JSON.stringify(data);
    }
    const resp = await fetch(`${url}`, fetchOptions);
    if (resp.status === 401) {
        const refreshResp = await fetch(`/api/auth/refresh`, {
            method: 'POST',
            credentials: 'include',
            headers: {
                Accept: 'application/json',
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({ refresh_token: localStorage.getItem('refresh_token') })
        });
        if (!refreshResp.ok) {
            console.error('Token refresh failed:', refreshResp.status);
            // 可添加登出逻辑
            localStorage.removeItem('token');
            localStorage.removeItem('refresh_token');
            window.location.href = '/login';
            return refreshResp;
        };
        const data = await refreshResp.json();
        localStorage.setItem('token', data.data.token);
        localStorage.setItem('refresh_token', data.data.refresh_token);
        // Retry original request once
        const retryOptions = { ...fetchOptions, headers: { ...fetchOptions.headers, Authorization: `Bearer ${data.data.token}` } };
        return await fetch(`${url}`, retryOptions);

    }
    return resp;
}