# Changelog

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
