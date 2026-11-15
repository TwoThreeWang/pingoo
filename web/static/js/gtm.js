// Google Tag Manager 的初始化：只在对外页面加载（见 internal/web/seo.go）。
// CSP 不允许页面里的内联脚本，所以放在这个文件里，要在 gtm.js 之前同步执行。
window.dataLayer = window.dataLayer || [];
function gtag() { dataLayer.push(arguments); }
dataLayer.push({ 'gtm.start': new Date().getTime(), event: 'gtm.js' });
gtag('js', new Date());
