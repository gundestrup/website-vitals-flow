# Website Vitals Flow

## Goal
Goal: https://webvitals.dev
but localhosted for testing prior to deployment
incl. http2/3 test - http compression - caching
inspiuration: https://www.crft.studio/lookup/gtmetrix-alternative

## Source code inspirtation
source code inspiration:
https://github.com/catchpoint/WebPageTest
https://github.com/GoogleChrome/web-vitals
https://github.com/bbc/web-vitals
https://github.com/googlechrome/lighthouse

## similar sources
other sources

https://runcloud.io/blog/gtmetrix-alternatives 

https://web.dev/articles/vitals#measure_core_web_vitals_in_javascript

https://github.com/GoogleChrome/web-vitals

https://github.com/bbc/web-vitals

https://dev.to/jacobandrewsky/measure-web-performance-with-web-vitals-5m7

https://github.com/techdivision/web-vitals

https://www.debugbear.com/test/website-speed

https://hub.docker.com/r/sitespeedio/sitespeed.io/

https://www.crft.studio/lookup/gtmetrix-alternative

Lighthouse

https://runcloud.io/blog/gtmetrix-alternatives

https://github.com/catchpoint/WebPageTest

## other info

Svend Gundestrup <svend@gundestrup.dk>
5 Oct 2026, 11:00
to Svend

https://webvitals.dev/
https://github.com/googlechrome/web-vitals

improve core vitals
https://dev-lab360.medium.com/the-ultimate-developers-guide-to-improving-core-web-vitals-lcp-fcp-cls-inp-beyond-3973c36bae41

https://github.com/bbc/web-vitals

https://github.com/GoogleChrome/lighthouse

Bulma
1. Optimize Bulma's CSS Size (Improves LCP & TTFB)
By default, compiling the entire Bulma library outputs a large CSS file, much of which you won't use.
Import only what you need: Instead of @import "bulma", configure your main Sass file (e.g., assets/css/styles.scss) to pull in individual components:
scss
---
---
// 1. Import utilities first
@import "bulma/sass/utilities/_all";

// 2. Set your custom variables
$primary: #8c1b1b;

// 3. Import only the components you actually use
@import "bulma/sass/base/_all";
@import "bulma/sass/elements/button";
@import "bulma/sass/elements/container";
@import "bulma/sass/grid/columns";
@import "bulma/sass/layout/section";
Use code with caution.

Minify CSS natively: Ensure Jekyll minifies your compiled CSS by adding this to your _config.yml: [1, 2]
yaml
sass:
  style: compressed
Use code with caution.

2. Eliminate Layout Shifts (Improves CLS)
Bulma uses a flexbox grid, which behaves predictable but can experience Cumulative Layout Shift if asynchronous elements or images populate late.
Use Bulma's Responsive Image Containers: Never drop a raw <img> tag into a column without reserving its aspect ratio. Use Bulma's explicit .image dimension helper classes to prevent the content below it from jumping: [1]
html
<figure class="image is-16x9">
  <img src="hero.jpg" alt="Hero image" loading="lazy">
</figure>
Use code with caution.

Avoid layout shift from JavaScript toggles: If you are using a mobile hamburger menu toggle via a lightweight script like Alpine.js, make sure elements are hidden by default via CSS classes rather than waiting for JS to execute. [1, 2]
3. Implement Critical CSS for Fold Optimization
For ideal Web Vitals scores, CSS required to render the top fold of your website should be inlined directly inside <style> tags in your <head>, while the rest of the Bulma layout rules are deferred.
You can utilize community automated workflows or node scripts via an asset pipeline to isolate critical CSS during the jekyll build phase. [1]
Monitoring Your Real-User Metrics
Keep in mind that standalone performance extensions like the legacy Web Vitals Extension have been fully integrated into the Chrome DevTools Performance Panel. You can inspect real-time shifts and metrics dynamically directly inside your browser during local Jekyll development (jekyll serve). [1, 2, 3, 4]
Are you experiencing a bottleneck with a specific Web Vitals metric (like LCP or CLS) on your Jekyll site, or are you looking to optimize a particular Bulma component?

static hosting
https://dev.to/alinaxiaoya/7-free-and-easy-to-use-static-web-hosting-services-4mc4