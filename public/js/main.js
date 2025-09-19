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