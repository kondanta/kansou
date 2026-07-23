# Changelog

## [1.10.0](https://github.com/kondanta/kansou/compare/v1.9.1...v1.10.0) (2026-07-23)


### Features

* **deps:** update dependency zizmor ( 1.27.0 → 1.28.0 ) ([#132](https://github.com/kondanta/kansou/issues/132)) ([783c7cb](https://github.com/kondanta/kansou/commit/783c7cbf98c274c6abe061e3cb0d7a2e637f69d6))


### Bug Fixes

* **ci:** make mise tool available on mise lock update ([#135](https://github.com/kondanta/kansou/issues/135)) ([a39d08f](https://github.com/kondanta/kansou/commit/a39d08fe37d71e3ba2149f8095193d50bf6cb60c))
* **ci:** make rennovate update mise lock file ([#134](https://github.com/kondanta/kansou/issues/134)) ([23ac898](https://github.com/kondanta/kansou/commit/23ac89899c2d5172f3e6d81108d278bc727a59db))


### Miscellaneous Chores

* **deps:** update deps ([#130](https://github.com/kondanta/kansou/issues/130)) ([828594d](https://github.com/kondanta/kansou/commit/828594deadf8a0f2176c98b3682b05730571c8d1))


### Code Refactoring

* **ci:** deduplicate gobuild process in release ([#133](https://github.com/kondanta/kansou/issues/133)) ([a305900](https://github.com/kondanta/kansou/commit/a305900f1a1498206affd5cf505e86b2afc01f91))

## [1.9.1](https://github.com/kondanta/kansou/compare/v1.9.0...v1.9.1) (2026-07-16)


### Bug Fixes

* **github-release:** update release sasalx/tribbie ( v0.4.1 → v0.4.2 ) ([#128](https://github.com/kondanta/kansou/issues/128)) ([7602553](https://github.com/kondanta/kansou/commit/7602553a1e11781203e89617897165bdabe9ef2d))

## [1.9.0](https://github.com/kondanta/kansou/compare/v1.8.0...v1.9.0) (2026-07-15)


### Features

* **chart:** enable InitContainers via extraInitContainers ([#111](https://github.com/kondanta/kansou/issues/111)) ([492d013](https://github.com/kondanta/kansou/commit/492d013101de16a88f2a0c22de692dadccf3724d))
* **deps:** update module github.com/lmittmann/tint ( v1.1.3 → v1.2.0 ) ([#113](https://github.com/kondanta/kansou/issues/113)) ([0e63239](https://github.com/kondanta/kansou/commit/0e63239128b2a2a2aa7c3811e966797d8032468e))
* **github-release:** update release sasalx/tribbie ( v0.3.1 → v0.4.1 ) ([#125](https://github.com/kondanta/kansou/issues/125)) ([b98b0e0](https://github.com/kondanta/kansou/commit/b98b0e04f17490165f3f2ff5093c06647d8ac418))
* **gorelease:** adopt gorelease ([#118](https://github.com/kondanta/kansou/issues/118)) ([55cd46d](https://github.com/kondanta/kansou/commit/55cd46d263c521de9dd5a85b359dbd076560534a))
* **server:** compare if remote genres configured locally ([#109](https://github.com/kondanta/kansou/issues/109)) ([ab3a417](https://github.com/kondanta/kansou/commit/ab3a417ef9736ad03666308ce2820dd65bb8615a))


### Bug Fixes

* **ci:** allow SirMergington to edit issues ([#123](https://github.com/kondanta/kansou/issues/123)) ([28c4842](https://github.com/kondanta/kansou/commit/28c4842707a95cae4d6e1e6d5cb24bd4a0e9024c))
* **ci:** give read vulnerability access to SirMergington ([#124](https://github.com/kondanta/kansou/issues/124)) ([e89d71b](https://github.com/kondanta/kansou/commit/e89d71bc554e0578b0d3677d2efe419d53163c57))
* **ci:** inline tint newhandler error ([#114](https://github.com/kondanta/kansou/issues/114)) ([4d69e67](https://github.com/kondanta/kansou/commit/4d69e6770452e6e78bd727a70e6c4fcea07cdd32))
* **github-release:** update release sasalx/tribbie ( v0.3.0 → v0.3.1 ) ([#105](https://github.com/kondanta/kansou/issues/105)) ([eccceb9](https://github.com/kondanta/kansou/commit/eccceb9c9253a6c5da18bb2f3db87e5731245018))
* **server:** return 400 for api requests that fallback to spaHandler ([#107](https://github.com/kondanta/kansou/issues/107)) ([76fd94b](https://github.com/kondanta/kansou/commit/76fd94b3824ffbec93e8bb438b964c1826fa6fe1))
* **server:** use snake case for response type ([#122](https://github.com/kondanta/kansou/issues/122)) ([f120bfa](https://github.com/kondanta/kansou/commit/f120bfa65f0f0ca4bd6302c5b206110ca75db9b9))


### Documentation

* add nice to have templates ([#119](https://github.com/kondanta/kansou/issues/119)) ([1933d4f](https://github.com/kondanta/kansou/commit/1933d4facc5719faaf7f8608c90e4ac393126789))


### Miscellaneous Chores

* go mod tidy ([#117](https://github.com/kondanta/kansou/issues/117)) ([7d67996](https://github.com/kondanta/kansou/commit/7d67996ff5801175bddddc518716029d4abac174))
* update deps ([#112](https://github.com/kondanta/kansou/issues/112)) ([a2e5f6e](https://github.com/kondanta/kansou/commit/a2e5f6e60afa1cc06c751334ee79d2df248294bf))


### Code Refactoring

* **linter:** fix newly added linter issues ([#110](https://github.com/kondanta/kansou/issues/110)) ([2265769](https://github.com/kondanta/kansou/commit/22657698f807cedc64022398f952c81627876147))

## [1.8.0](https://github.com/kondanta/kansou/compare/v1.7.0...v1.8.0) (2026-07-09)


### Features

* **charts:** add envFrom to values ([#102](https://github.com/kondanta/kansou/issues/102)) ([e4aedce](https://github.com/kondanta/kansou/commit/e4aedcee0e2b29e154e94a5658b982921ea70f0e))

## [1.7.0](https://github.com/kondanta/kansou/compare/v1.6.1...v1.7.0) (2026-07-09)


### Features

* **charts:** add defaultPodOptions ([#101](https://github.com/kondanta/kansou/issues/101)) ([6d8a7e4](https://github.com/kondanta/kansou/commit/6d8a7e4c1a4f5b75c2542579d5cfc3d85721c21f))
* **github-release:** update release sasalx/tribbie ( v0.2.0 → v0.3.0 ) ([#88](https://github.com/kondanta/kansou/issues/88)) ([2c2d7a1](https://github.com/kondanta/kansou/commit/2c2d7a1de074abfe965c627657bce8eaa27f6db3))
* **history:** enable server to perform HardDelete for history ([#91](https://github.com/kondanta/kansou/issues/91)) ([b1ef507](https://github.com/kondanta/kansou/commit/b1ef50773a1e089c5c70588fd446d1e340232b2d))
* **history:** enable switching between previous scores ([#97](https://github.com/kondanta/kansou/issues/97)) ([0609e48](https://github.com/kondanta/kansou/commit/0609e48a59bdff819ab6d3e49cbb11c4141fbaa7))
* **tint:** add tint for local development ([#100](https://github.com/kondanta/kansou/issues/100)) ([ed3e1d7](https://github.com/kondanta/kansou/commit/ed3e1d73a42bda5d929fa3302155914c6f03e1b7))


### Bug Fixes

* **container:** update image golang ( 1.26.4 → 1.26.5 ) ([#93](https://github.com/kondanta/kansou/issues/93)) ([51ee57c](https://github.com/kondanta/kansou/commit/51ee57c888bb906a460fcd2aed84ac31d363f9a4))
* **dependency:** bump crypto transitive dependency ([#96](https://github.com/kondanta/kansou/issues/96)) ([1127264](https://github.com/kondanta/kansou/commit/1127264483e778a1d988d5fccc59c64280f3610a))
* **server:** allow DELETE verb in Access-Control-Allow-Methods  ([#87](https://github.com/kondanta/kansou/issues/87)) ([bf74b1c](https://github.com/kondanta/kansou/commit/bf74b1c06b1cc5eec28597c026be9b14b6390ddc))
* **store:** make score nillable ([#85](https://github.com/kondanta/kansou/issues/85)) ([5fb0b50](https://github.com/kondanta/kansou/commit/5fb0b5080dc6a9421636a20276c50b5125849df1))


### Miscellaneous Chores

* bump go version ([#95](https://github.com/kondanta/kansou/issues/95)) ([dda6080](https://github.com/kondanta/kansou/commit/dda60806169bab168965f27f15c322f9f7019309))


### Code Refactoring

* **log:** add structured logging ([#92](https://github.com/kondanta/kansou/issues/92)) ([8b0533c](https://github.com/kondanta/kansou/commit/8b0533c5e4427b08bed8f99162c6dffa9d1bd4c1))

## [1.6.1](https://github.com/kondanta/kansou/compare/v1.6.0...v1.6.1) (2026-07-06)


### Miscellaneous Chores

* bump charts ([#83](https://github.com/kondanta/kansou/issues/83)) ([b4bdf82](https://github.com/kondanta/kansou/commit/b4bdf8274cfedd5352cff180841b1aadb4179882))

## [1.6.0](https://github.com/kondanta/kansou/compare/v1.5.0...v1.6.0) (2026-07-06)


### Features

* **store:** show how many entries media has in /history ([#79](https://github.com/kondanta/kansou/issues/79)) ([1815c14](https://github.com/kondanta/kansou/commit/1815c14cd0f80e28e7ae63452aee94bdf4e4d26c))


### Bug Fixes

* **ci:** do not export tribbie to /web/dist as it already has dist ([#71](https://github.com/kondanta/kansou/issues/71)) ([ca062d6](https://github.com/kondanta/kansou/commit/ca062d68c83c8338c6d10325759e8a1c37cd42eb))
* **config:** show MaxHistory data ([#74](https://github.com/kondanta/kansou/issues/74)) ([51962bb](https://github.com/kondanta/kansou/commit/51962bb216724b6b3841bdebeb172ef5be8dad48))
* **config:** use default maxHistory if it is empty ([#82](https://github.com/kondanta/kansou/issues/82)) ([3288fee](https://github.com/kondanta/kansou/commit/3288feebe041f73da6fe2814a635b85f80e2da6b))
* **deps:** update module github.com/go-chi/chi/v5 ( v5.3.0 → v5.3.1 ) ([#77](https://github.com/kondanta/kansou/issues/77)) ([1cbf7ea](https://github.com/kondanta/kansou/commit/1cbf7ea765af23a8174d707ca8a255b73a5f6d7d))
* **score:** add missing coverimage field ([#78](https://github.com/kondanta/kansou/issues/78)) ([adf0962](https://github.com/kondanta/kansou/commit/adf0962e3a6683f7f23c8b44134e5a7c3dee2d13))
* **store:** create seed error for checking if db seeded ([#80](https://github.com/kondanta/kansou/issues/80)) ([1e8d0e2](https://github.com/kondanta/kansou/commit/1e8d0e2a5afe786d504904d08d121f3deda9a746))
* **store:** export store types as json and add coverimage ([#75](https://github.com/kondanta/kansou/issues/75)) ([ab1f949](https://github.com/kondanta/kansou/commit/ab1f94975e67da882a538361a8e0b55bad2ad38a))
* **store:** handle seed error ([#81](https://github.com/kondanta/kansou/issues/81)) ([9484c42](https://github.com/kondanta/kansou/commit/9484c424a2551af7abb78788f44528a47b9bf2bf))


### Documentation

* **swagger:** regenerate swagger ([#76](https://github.com/kondanta/kansou/issues/76)) ([9dea4ee](https://github.com/kondanta/kansou/commit/9dea4ee7531e5fec6bf109df6302efaa509d4aa2))


### Miscellaneous Chores

* update dependencies ([#73](https://github.com/kondanta/kansou/issues/73)) ([1dd2a58](https://github.com/kondanta/kansou/commit/1dd2a586270098eb518cda8b7e664a6273ef93ce))

## [1.5.0](https://github.com/kondanta/kansou/compare/v1.4.0...v1.5.0) (2026-07-03)


### Features

* adding history feature to kansou ([#60](https://github.com/kondanta/kansou/issues/60)) ([5bb4aaa](https://github.com/kondanta/kansou/commit/5bb4aaae192a9bf0446e8147a95979052e4df504))
* **deps:** update module github.com/go-chi/httprate ( v0.15.0 → v0.16.0 ) ([#66](https://github.com/kondanta/kansou/issues/66)) ([312479c](https://github.com/kondanta/kansou/commit/312479cdc11b68688a13bff1bfc1960cba5f8060))
* **github-release:** update release sasalx/tribbie ( v0.1.0 → v0.2.0 ) ([#59](https://github.com/kondanta/kansou/issues/59)) ([836a842](https://github.com/kondanta/kansou/commit/836a842272f6ff1bd970ac5d532e0833de74ff99))


### Bug Fixes

* **ci:** fix linter issues ([#56](https://github.com/kondanta/kansou/issues/56)) ([3dcabce](https://github.com/kondanta/kansou/commit/3dcabce209421d37aa8ac51b63ce977d25228d52))
* do not override chart version with the app-version ([#53](https://github.com/kondanta/kansou/issues/53)) ([a75d308](https://github.com/kondanta/kansou/commit/a75d308cd76f8a96686b78303a19dda30896d07b))


### Documentation

* **swagger:** update swagger ([#64](https://github.com/kondanta/kansou/issues/64)) ([60fb095](https://github.com/kondanta/kansou/commit/60fb095a2618cc702a3110ed978e0483b8573b0a))


### Miscellaneous Chores

* bump go version  ([85f2ccf](https://github.com/kondanta/kansou/commit/85f2ccf1637080246255a96022abc7ab5c155f7f))


### Code Refactoring

* enable modernize on linter ([#68](https://github.com/kondanta/kansou/issues/68)) ([b6772d3](https://github.com/kondanta/kansou/commit/b6772d3b56687ad68ac463a6beff6869a220bf9c))
* put all api routes behind /api  ([#67](https://github.com/kondanta/kansou/issues/67)) ([d3e8826](https://github.com/kondanta/kansou/commit/d3e8826e617d1795bda57d0ac0628f8be596be66))
* put api behind version ([#69](https://github.com/kondanta/kansou/issues/69)) ([33e011c](https://github.com/kondanta/kansou/commit/33e011c00e3655bd5249a3849ef25b482e3ced8b))
