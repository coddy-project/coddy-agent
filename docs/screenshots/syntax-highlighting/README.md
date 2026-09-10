# Syntax highlighting screenshots

Captured from the running Vite build with the real ChatScreen and Markdown components and a deterministic JavaScript, CSS, HTML, and JSON transcript. The before captures use the styles from main at 386a0df; after captures use this change. No live provider or user conversation is used.

All seven themes were checked at 390 × 900 and 1280 × 900. Header, transcript-column, and composer edges align at both widths, and the page does not overflow horizontally.

## 1280px viewport

| Theme | Before | After |
| --- | --- | --- |
| dark | ![dark before](before-dark-1280.png) | ![dark after](after-dark-1280.png) |
| light | ![light before](before-light-1280.png) | ![light after](after-light-1280.png) |
| midnight | ![midnight before](before-midnight-1280.png) | ![midnight after](after-midnight-1280.png) |
| solarized-dark | ![solarized-dark before](before-solarized-dark-1280.png) | ![solarized-dark after](after-solarized-dark-1280.png) |
| monokai | ![monokai before](before-monokai-1280.png) | ![monokai after](after-monokai-1280.png) |
| nord | ![nord before](before-nord-1280.png) | ![nord after](after-nord-1280.png) |
| rose-pine | ![rose-pine before](before-rose-pine-1280.png) | ![rose-pine after](after-rose-pine-1280.png) |

## 390px viewport

| Theme | Before | After |
| --- | --- | --- |
| dark | ![dark before](before-dark-390.png) | ![dark after](after-dark-390.png) |
| light | ![light before](before-light-390.png) | ![light after](after-light-390.png) |
| midnight | ![midnight before](before-midnight-390.png) | ![midnight after](after-midnight-390.png) |
| solarized-dark | ![solarized-dark before](before-solarized-dark-390.png) | ![solarized-dark after](after-solarized-dark-390.png) |
| monokai | ![monokai before](before-monokai-390.png) | ![monokai after](after-monokai-390.png) |
| nord | ![nord before](before-nord-390.png) | ![nord after](after-nord-390.png) |
| rose-pine | ![rose-pine before](before-rose-pine-390.png) | ![rose-pine after](after-rose-pine-390.png) |

## PostCSS language alias regression

The `postcss` follow-up uses the same running components and stylesheet on both sides. Before disables only the new alias, reproducing the previously uncolored block; after enables it. Selectors, properties, numbers, and comments are tokenized, while the original source and `language-postcss` label are retained.

| View | Before | After |
| --- | --- | --- |
| dark, 1280px | ![PostCSS before](postcss-before-dark-1280.png) | ![PostCSS after](postcss-after-dark-1280.png) |
| light, 1280px | ![PostCSS before](postcss-before-light-1280.png) | ![PostCSS after](postcss-after-light-1280.png) |
| dark, 390px | ![PostCSS before](postcss-before-dark-390.png) | ![PostCSS after](postcss-after-dark-390.png) |
| light, 390px | ![PostCSS before](postcss-before-light-390.png) | ![PostCSS after](postcss-after-light-390.png) |

## Vue language alias regression

The Vue follow-up captures real component markup with a bound attribute, comments, script setup, and scoped CSS. Before disables only the Vue alias; after enables it. Standard JavaScript and CSS inside script/style tags are highlighted. Vue expressions and alternative language preprocessors do not have dedicated parsers.

| View | Before | After |
| --- | --- | --- |
| dark, 1280px | ![Vue before](vue-before-dark-1280.png) | ![Vue after](vue-after-dark-1280.png) |
| light, 1280px | ![Vue before](vue-before-light-1280.png) | ![Vue after](vue-after-light-1280.png) |
| dark, 390px | ![Vue before](vue-before-dark-390.png) | ![Vue after](vue-after-dark-390.png) |
| light, 390px | ![Vue before](vue-before-light-390.png) | ![Vue after](vue-after-light-390.png) |
