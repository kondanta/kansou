# Changelog

## [2.0.0](https://github.com/kondanta/kansou/compare/v1.4.0...v2.0.0) (2026-06-28)


### ⚠ BREAKING CHANGES

* **github-action:** Update action golangci/golangci-lint-action ( v6 → v9.2.1 ) ([#47](https://github.com/kondanta/kansou/issues/47))
* **github-action:** Update action googleapis/release-please-action ( v4 → v5 ) ([#48](https://github.com/kondanta/kansou/issues/48))
* **github-action:** Update action actions/checkout ( v4 → v7 ) ([#12](https://github.com/kondanta/kansou/issues/12))
* **github-action:** Update GitHub Artifact Actions ([#24](https://github.com/kondanta/kansou/issues/24))
* **github-action:** Update action azure/setup-helm ( v4 → v5.0.1 ) ([#15](https://github.com/kondanta/kansou/issues/15))
* **github-action:** Update action softprops/action-gh-release ( v2 → v3.0.1 ) ([#23](https://github.com/kondanta/kansou/issues/23))
* **deps:** Update module github.com/swaggo/http-swagger ( v1.3.4 → v2.0.2 ) ([#26](https://github.com/kondanta/kansou/issues/26))
* **github-action:** Update action actions/setup-node ( v4 → v6.4.0 ) ([#35](https://github.com/kondanta/kansou/issues/35))
* **github-action:** Update action actions/setup-go ( v5 → v6.5.0 ) ([#34](https://github.com/kondanta/kansou/issues/34))
* **github-action:** Update docker github actions ([#36](https://github.com/kondanta/kansou/issues/36))

### Features

* add ceiling for max_multiplier and remove score publish CLI command ([6f46c73](https://github.com/kondanta/kansou/commit/6f46c73ffab2a204159c2dfc6e8c4d064cf2ae7e))
* add ci ([4abf5e2](https://github.com/kondanta/kansou/commit/4abf5e2e9c272861a35bc33be09a5320377cc132))
* add helm chart ([9b64217](https://github.com/kondanta/kansou/commit/9b642170932130c0e3812f538e5b78885a083254))
* add helmcharts as artifact ([#4](https://github.com/kondanta/kansou/issues/4)) ([ae1406c](https://github.com/kondanta/kansou/commit/ae1406c3635180d7947b390018959e7b353b788d))
* add mock webui for testing server ([b0d8c4b](https://github.com/kondanta/kansou/commit/b0d8c4bd27d2d35f25bf7436cf4c020e33e037ec))
* add per-IP rate limiting on AniList-proxying endpoints ([a5f0511](https://github.com/kondanta/kansou/commit/a5f05114190c10de78d4a9868534b5e3323d2014))
* add renovate ([#6](https://github.com/kondanta/kansou/issues/6)) ([ab46675](https://github.com/kondanta/kansou/commit/ab4667531d37537dc003afd552f61ec3d7134c3f))
* **anilist:** include banner image as well ([c243d3d](https://github.com/kondanta/kansou/commit/c243d3daf2f1f04f359bb48244ad6163eff46af0))
* auto publish breakdown to anilist as notes ([79ba17e](https://github.com/kondanta/kansou/commit/79ba17e4405c655ad61af1c41780fbd55ebb2d0f))
* build tribbie Vue UI in release workflow via actions/setup-node ([849cfa6](https://github.com/kondanta/kansou/commit/849cfa6351bf17b963f4699c47b59e6365abf73b))
* **ci:** make sirmergington to be able to cut a release ([#44](https://github.com/kondanta/kansou/issues/44)) ([92e46cf](https://github.com/kondanta/kansou/commit/92e46cfa8abe08b881989e727050dd5304f52116))
* **container:** update image alpine ( 3.19 → 3.24 ) ([#11](https://github.com/kondanta/kansou/issues/11)) ([1d82288](https://github.com/kondanta/kansou/commit/1d822888db38cc79c229ebe7c134bab86978df30))
* **deps:** Update module github.com/swaggo/http-swagger ( v1.3.4 → v2.0.2 ) ([#26](https://github.com/kondanta/kansou/issues/26)) ([609624f](https://github.com/kondanta/kansou/commit/609624f40cf66fb97ef854ac8fe8428529009e59))
* embed sasalx/tribbie webui ([ec2cede](https://github.com/kondanta/kansou/commit/ec2cede7159299b9077afac93a04515c225cd219))
* expose effective weight and sum to API ([11f5878](https://github.com/kondanta/kansou/commit/11f5878cbb1607c63d4c3d6bbfc44727a656a754))
* expose intermediate calculations for /weight endpoint ([46030ff](https://github.com/kondanta/kansou/commit/46030ffabdbcc7f86dae41c458140abb43760411))
* expose secondary genres multiplier to api as well ([d050d7b](https://github.com/kondanta/kansou/commit/d050d7be49e23bdb02b9c41a574394716c7c2a3c))
* introduce primary genre and solve genre dilution issue ([41f561a](https://github.com/kondanta/kansou/commit/41f561a9e4b5fc755a4ce2a918d7e468b15f362e))
* **live-config:** add live config ([#3](https://github.com/kondanta/kansou/issues/3)) ([a7f5ba4](https://github.com/kondanta/kansou/commit/a7f5ba4475d2777c624caf0663e55df3481e874f))
* **search:** search returns to an array instead of single object ([2d5b573](https://github.com/kondanta/kansou/commit/2d5b573bce3b128eefc9c3c00a9a517261433cc3))
* vibecode the whole thing ([eb89dcb](https://github.com/kondanta/kansou/commit/eb89dcb23cc14d2e1f52febba866cf37146c5fa1))


### Bug Fixes

* add container and pod security contexts to Deployment ([4401ce1](https://github.com/kondanta/kansou/commit/4401ce119537dccc5c9ee2bba949c8b89a2eaf1b))
* add just command for delete/readding tribbie submodule ([2af4e8c](https://github.com/kondanta/kansou/commit/2af4e8c9575534881156e46f995d783b8011b0b3))
* add X-Content-Type-Options and X-Frame-Options security headers ([b6bb650](https://github.com/kondanta/kansou/commit/b6bb650529f97c10937bbfe691d16a6a0d63bab5))
* cap POST request body at 1 MB, return 413 on overflow ([1336121](https://github.com/kondanta/kansou/commit/133612148addb38d3182ddb30b9a2f141bb3c24a))
* **charts:** add validation ([53b87cc](https://github.com/kondanta/kansou/commit/53b87cc0f6d0a8bb6747679a51af577a343c6039))
* **ci:** add pnpm first ([ae2cd3b](https://github.com/kondanta/kansou/commit/ae2cd3b8fd6099f6a168c8ad9e141ad1c8e77642))
* **ci:** clone tribbie directly instead of relying on submodule init ([1872635](https://github.com/kondanta/kansou/commit/187263588a30b90e94fc75d2775ad4a10c16e01f))
* **ci:** make github updates to not use ! for version bumps ([#52](https://github.com/kondanta/kansou/issues/52)) ([7a68c47](https://github.com/kondanta/kansou/commit/7a68c47243ae838ba92060682a712284d26e8f9b))
* **ci:** remove pnpm cache from setup-node in release workflow ([f8f8b80](https://github.com/kondanta/kansou/commit/f8f8b80cea626244f1a334c51e9cfea084e5d517))
* **ci:** specify pnpm version explicitly in release workflow ([a18f7cf](https://github.com/kondanta/kansou/commit/a18f7cff81137d45d1574f9333d0308782979f8d))
* **ci:** upgrade pnpm to 10.33.0 ([14411ae](https://github.com/kondanta/kansou/commit/14411ae5b61a598cad3dca60720192e36088b38f))
* **ci:** use --no-frozen-lockfile for tribbie pnpm install ([2a286d5](https://github.com/kondanta/kansou/commit/2a286d5f5487b22d821b70e134ebe4c41d702dd4))
* **container:** update image golang ( 1.26.1 → 1.26.4 ) ([#32](https://github.com/kondanta/kansou/issues/32)) ([e0b76ab](https://github.com/kondanta/kansou/commit/e0b76ab6ddfb276694aa665a9f9ebb801909b38a))
* handle io.ReadAll error and cap AniList error response body ([1da4ee6](https://github.com/kondanta/kansou/commit/1da4ee61e0cbc9449ffa8ae510fd8826698b6fb8))
* hide AniList upstream errors from client, log server-side ([22db282](https://github.com/kondanta/kansou/commit/22db282178b609e8656e57fb49e5f380d6931415))
* idiomatic Go — any, return errors from RunE, remove dead code ([afb7289](https://github.com/kondanta/kansou/commit/afb72890e46969a39f8a779973bca16ca2e8b2fd))
* keep dist dir ([bb089b8](https://github.com/kondanta/kansou/commit/bb089b8115f6bb461e4dfe75eead25e7a094d86f))
* reject NaN and Inf score values in POST /score ([7f8c3e6](https://github.com/kondanta/kansou/commit/7f8c3e6c495d5022ff4b821a0a030ca4fee4a619))
* **renovate:** group go version updates ([#41](https://github.com/kondanta/kansou/issues/41)) ([d9b4926](https://github.com/kondanta/kansou/commit/d9b4926ae827155c843b81d4beafdc28c1b8ef15))
* **renovate:** group releases meaningfully ([#29](https://github.com/kondanta/kansou/issues/29)) ([373f3df](https://github.com/kondanta/kansou/commit/373f3df3d2f30af1f3ecd8da6aff0ed3e01348a0))
* score range validation, CORS case normalization, negative ID guard, engine dedup ([e2969f3](https://github.com/kondanta/kansou/commit/e2969f392c12125cb8c9ae3c6c2598f981192cac))
* **scoring:** send raw contribution values instead of pre-rounded ([3fe7771](https://github.com/kondanta/kansou/commit/3fe7771e71818ce4d3f276a1142e43391632317d))
* **scoring:** when primary is the sole genre ignore blend ratio ([7a7a420](https://github.com/kondanta/kansou/commit/7a7a4206af339cea31448a95783cc0f797089a0e))
* set default resource limits and add Vary: Origin CORS header ([f3b19ee](https://github.com/kondanta/kansou/commit/f3b19eeedd2c2baed6cbeb3264526c20d62bb4ef))
* set read/write/idle timeouts on http.Server ([339229a](https://github.com/kondanta/kansou/commit/339229ad4615919194c1f4969692848938749fee))
* shallow pull instead of submodule shenanigans ([09a7527](https://github.com/kondanta/kansou/commit/09a7527402383b225574deb8a310c1e4c698e4ff))
* **swagger:** bump swaggo dependency ([#43](https://github.com/kondanta/kansou/issues/43)) ([357c0b5](https://github.com/kondanta/kansou/commit/357c0b5b728d577c456878dab130827bb850561b))
* **swagger:** remove hard-coded localhost ([#5](https://github.com/kondanta/kansou/issues/5)) ([0d0707e](https://github.com/kondanta/kansou/commit/0d0707e6eab5f1bf1bdb3eee870b0e53d71b7817))
* use client id again ([#51](https://github.com/kondanta/kansou/issues/51)) ([7724e3e](https://github.com/kondanta/kansou/commit/7724e3e6bc30b4712030d835535583a7163c10a8))
* webui directories ([75a02c1](https://github.com/kondanta/kansou/commit/75a02c1aa3ed0a6c83edbe4c6d4d7f72611827ff))


### Documentation

* add licenses and fix swagger field ([35bf1be](https://github.com/kondanta/kansou/commit/35bf1bed1a81fef277b2cbde3b24049eaf4a1ab8))
* **adr:** add interesting feature idea ([76aad6d](https://github.com/kondanta/kansou/commit/76aad6db470ba5fa169852999c57deb7e96b8b0d))
* **readme:** add renormalization math ([84eae52](https://github.com/kondanta/kansou/commit/84eae523f72d835a60d72086f27a3f1c7e78394e))
* update docs for exposing effective weight ([2c3819e](https://github.com/kondanta/kansou/commit/2c3819ee1d9f17a7e316c1380568688fc21c6e59))


### Miscellaneous Chores

* add copy/pastable text to test web ui ([94b2d7d](https://github.com/kondanta/kansou/commit/94b2d7d034f07e9ee50760106019cbdc92a552be))
* **anilist:** do not change the status of the media ([9b861d9](https://github.com/kondanta/kansou/commit/9b861d9d7f0e9da8a7a7bf74971973055fdb70e4))
* **config:** migrate config .renovaterc.json5 ([#27](https://github.com/kondanta/kansou/issues/27)) ([e4f41fd](https://github.com/kondanta/kansou/commit/e4f41fd7c12c921a18fae0be756e628f39224c29))
* **config:** migrate config .renovaterc.json5 ([#38](https://github.com/kondanta/kansou/issues/38)) ([d207e7d](https://github.com/kondanta/kansou/commit/d207e7d015c84066673b5b53f943db8331bac107))
* forcefully update to v1.4.0 instead of bot creating 2.0.0 ([5a95f37](https://github.com/kondanta/kansou/commit/5a95f37843479aa52b0c20f664c62ccdec500cca))
* refactor cmd structure ([30c2548](https://github.com/kondanta/kansou/commit/30c25482b4bdb1e47d23f5d9d7f3c92fe78bdb1f))
* refactor minor things ([#1](https://github.com/kondanta/kansou/issues/1)) ([be34a3e](https://github.com/kondanta/kansou/commit/be34a3e4d033ea99109eec2dffc83d5a4168455b))
* update cli interface for primary genre  ([721be30](https://github.com/kondanta/kansou/commit/721be306a726920c8c48fc83158ebbdd54050f78))
* update dependencies ([1ee917b](https://github.com/kondanta/kansou/commit/1ee917b8892bea4b891ca037403d9b126c93f659))
* update dependencies ([6061cb4](https://github.com/kondanta/kansou/commit/6061cb484455cd552a7746018695050e944b6cc3))
* update dependencies ([b3ee922](https://github.com/kondanta/kansou/commit/b3ee922dd5a6036bc05064ed2b1f2163c3bfdf55))
* update dependencies ([e3e5680](https://github.com/kondanta/kansou/commit/e3e5680abae129dc0b843d79ff347f1d96e787db))
* update deps ([d57c183](https://github.com/kondanta/kansou/commit/d57c1832353e529a5d2ecf05b11f1429d97f0d62))
* use bot app id instead of client id ([#49](https://github.com/kondanta/kansou/issues/49)) ([90f2a89](https://github.com/kondanta/kansou/commit/90f2a8906db973ce60ea39ecc937e792edd857cf))


### Code Refactoring

* expose blend ratio in /weights ([0358028](https://github.com/kondanta/kansou/commit/0358028c4dcc27ef448b30e7499ae693848c57cf))
* simplify handler ([8d4eca1](https://github.com/kondanta/kansou/commit/8d4eca144b1ccf2bd199191c63a2a3511ea839e8))
* use strings.ToLower instead of custom ToLower ([11b5f02](https://github.com/kondanta/kansou/commit/11b5f028bd1ba8fd3894775e179ced679e8dd272))


### Continuous Integration

* **github-action:** Update action actions/checkout ( v4 → v7 ) ([#12](https://github.com/kondanta/kansou/issues/12)) ([20906e1](https://github.com/kondanta/kansou/commit/20906e19cae689091bc05b2c27658bf4ac557baf))
* **github-action:** Update action actions/setup-go ( v5 → v6.5.0 ) ([#34](https://github.com/kondanta/kansou/issues/34)) ([a0d120f](https://github.com/kondanta/kansou/commit/a0d120f1dea91f5aef4e96a90e754b24a318f422))
* **github-action:** Update action actions/setup-node ( v4 → v6.4.0 ) ([#35](https://github.com/kondanta/kansou/issues/35)) ([a00d773](https://github.com/kondanta/kansou/commit/a00d7733c411c0b37beebf167e2e1327ed8feb42))
* **github-action:** Update action azure/setup-helm ( v4 → v5.0.1 ) ([#15](https://github.com/kondanta/kansou/issues/15)) ([acca0db](https://github.com/kondanta/kansou/commit/acca0dbae165638e3a39cc20d5f4cdb9cd027209))
* **github-action:** Update action golangci/golangci-lint-action ( v6 → v9.2.1 ) ([#47](https://github.com/kondanta/kansou/issues/47)) ([95af94e](https://github.com/kondanta/kansou/commit/95af94e928d5733f7a1f06042780e6243835ebab))
* **github-action:** Update action googleapis/release-please-action ( v4 → v5 ) ([#48](https://github.com/kondanta/kansou/issues/48)) ([e369080](https://github.com/kondanta/kansou/commit/e369080743f1a0e3c1bf0c9828edf7c4bfa03a27))
* **github-action:** Update action softprops/action-gh-release ( v2 → v3.0.1 ) ([#23](https://github.com/kondanta/kansou/issues/23)) ([1747f6c](https://github.com/kondanta/kansou/commit/1747f6c271ab4e02b704ec8333a2f80bfa04457f))
* **github-action:** Update docker github actions ([#36](https://github.com/kondanta/kansou/issues/36)) ([a0670d0](https://github.com/kondanta/kansou/commit/a0670d0b96380d7f368dd9e05a7fef8667e04dfb))
* **github-action:** Update GitHub Artifact Actions ([#24](https://github.com/kondanta/kansou/issues/24)) ([ef651dc](https://github.com/kondanta/kansou/commit/ef651dc39cc2d286dde4955a81ba171470dc1e52))
