/* 在页面画出来之前套上保存的主题，避免先闪一下默认主题。单独放一个不带 defer 的小文件，
   这样页面里不需要内联脚本，CSP 可以只允许本站的脚本文件。 */
try { var t = localStorage.getItem('pingoo-theme'); if (t && t !== 'auto') document.documentElement.dataset.theme = t; } catch (e) {}
