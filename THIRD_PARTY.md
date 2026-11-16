# Third-party components

Browser dependencies are bundled with their upstream licenses in `web/vendor`. The running application serves the exact embedded copies locally. Source dependencies for Go are under `vendor/golang.org/x/`.

| Component | Version | License file |
| --- | --- | --- |
| PDF.js | 4.10.38 | `web/vendor/pdfjs-dist/LICENSE` |
| Mammoth.js | 1.11.0 | `web/vendor/mammoth/LICENSE` |
| SheetJS CE | 0.20.3 | `web/vendor/xlsx/LICENSE` |
| DOMPurify | 3.3.3 | `web/vendor/dompurify/LICENSE` |
| marked | 15.0.12 | `web/vendor/marked/LICENSE.md` |
| highlight.js | 11.11.1 | `web/vendor/cdn-assets/LICENSE` |
| Inter Variable | 5.2.6 (Fontsource package) | `web/vendor/inter/LICENSE` |
| Noto Sans SC Variable | 5.2.10 (Fontsource package) | `web/vendor/noto-sans-sc/LICENSE` |
| libheif-js (with bundled WebAssembly runtime) | 1.23.5 | `web/vendor/libheif-js/LICENSE` and `web/vendor/libheif-js/LICENSE.runtime` |
| golang.org/x/sys | 0.38.0 | `vendor/golang.org/x/sys/LICENSE` |
| golang.org/x/term | 0.37.0 | `vendor/golang.org/x/term/LICENSE` |

The JavaScript bundles may contain additional components whose notices are included by the corresponding upstream distributions.

The HEIF module source is available from [libheif-js](https://github.com/catdad-experiments/libheif-js) and [libheif](https://github.com/strukturag/libheif). Compatible video previews may call a server-provided [FFmpeg](https://ffmpeg.org/) installation.
