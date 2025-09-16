// Populate the sidebar
//
// This is a script, and not included directly in the page, to control the total size of the book.
// The TOC contains an entry for each page, so if each page includes a copy of the TOC,
// the total size of the page becomes O(n**2).
class MDBookSidebarScrollbox extends HTMLElement {
    constructor() {
        super();
    }
    connectedCallback() {
        this.innerHTML = '<ol class="chapter"><li class="chapter-item expanded affix "><a href="index.html">MOCO</a></li><li class="chapter-item expanded affix "><li class="part-title">User manual</li><li class="chapter-item expanded "><a href="getting_started.html"><strong aria-hidden="true">1.</strong> Getting started</a></li><li><ol class="section"><li class="chapter-item expanded "><a href="setup.html"><strong aria-hidden="true">1.1.</strong> Deploying MOCO</a></li><li class="chapter-item expanded "><a href="helm.html"><strong aria-hidden="true">1.2.</strong> Helm Chart</a></li><li class="chapter-item expanded "><a href="install-plugin.html"><strong aria-hidden="true">1.3.</strong> Installing kubectl-moco</a></li></ol></li><li class="chapter-item expanded "><a href="usage.html"><strong aria-hidden="true">2.</strong> Usage</a></li><li class="chapter-item expanded "><a href="advanced.html"><strong aria-hidden="true">3.</strong> Advanced topics</a></li><li><ol class="section"><li class="chapter-item expanded "><a href="custom-mysqld.html"><strong aria-hidden="true">3.1.</strong> Building your own image</a></li><li class="chapter-item expanded "><a href="customize-system-container.html"><strong aria-hidden="true">3.2.</strong> Customize system container</a></li><li class="chapter-item expanded "><a href="change-pvc-template.html"><strong aria-hidden="true">3.3.</strong> Change volumeClaimTemplates</a></li><li class="chapter-item expanded "><a href="rolling-update-strategy.html"><strong aria-hidden="true">3.4.</strong> Rollout strategy</a></li></ol></li><li class="chapter-item expanded "><a href="known_issues.html"><strong aria-hidden="true">4.</strong> Known issues</a></li><li class="chapter-item expanded affix "><li class="part-title">References</li><li class="chapter-item expanded "><a href="crd.html"><strong aria-hidden="true">5.</strong> Custom resources</a></li><li><ol class="section"><li class="chapter-item expanded "><a href="crd_mysqlcluster_v1beta2.html"><strong aria-hidden="true">5.1.</strong> MySQLCluster v1beta2</a></li><li class="chapter-item expanded "><a href="crd_backuppolicy_v1beta2.html"><strong aria-hidden="true">5.2.</strong> BackupPolicy v1beta2</a></li></ol></li><li class="chapter-item expanded "><a href="commands.html"><strong aria-hidden="true">6.</strong> Commands</a></li><li><ol class="section"><li class="chapter-item expanded "><a href="kubectl-moco.html"><strong aria-hidden="true">6.1.</strong> kubectl-moco</a></li><li class="chapter-item expanded "><a href="moco-controller.html"><strong aria-hidden="true">6.2.</strong> moco-controller</a></li><li class="chapter-item expanded "><a href="moco-backup.html"><strong aria-hidden="true">6.3.</strong> moco-backup</a></li></ol></li><li class="chapter-item expanded "><a href="metrics.html"><strong aria-hidden="true">7.</strong> Metrics</a></li><li class="chapter-item expanded affix "><li class="part-title">Developer documents</li><li class="chapter-item expanded "><a href="notes.html"><strong aria-hidden="true">8.</strong> Design notes</a></li><li><ol class="section"><li class="chapter-item expanded "><a href="design.html"><strong aria-hidden="true">8.1.</strong> Goals</a></li><li class="chapter-item expanded "><a href="reconcile.html"><strong aria-hidden="true">8.2.</strong> Reconciliation</a></li><li class="chapter-item expanded "><a href="clustering.html"><strong aria-hidden="true">8.3.</strong> Clustering</a></li><li class="chapter-item expanded "><a href="backup.html"><strong aria-hidden="true">8.4.</strong> Backup and restore</a></li><li class="chapter-item expanded "><a href="upgrading.html"><strong aria-hidden="true">8.5.</strong> Upgrading mysqld</a></li><li class="chapter-item expanded "><a href="security.html"><strong aria-hidden="true">8.6.</strong> Security</a></li></ol></li></ol>';
        // Set the current, active page, and reveal it if it's hidden
        let current_page = document.location.href.toString().split("#")[0].split("?")[0];
        if (current_page.endsWith("/")) {
            current_page += "index.html";
        }
        var links = Array.prototype.slice.call(this.querySelectorAll("a"));
        var l = links.length;
        for (var i = 0; i < l; ++i) {
            var link = links[i];
            var href = link.getAttribute("href");
            if (href && !href.startsWith("#") && !/^(?:[a-z+]+:)?\/\//.test(href)) {
                link.href = path_to_root + href;
            }
            // The "index" page is supposed to alias the first chapter in the book.
            if (link.href === current_page || (i === 0 && path_to_root === "" && current_page.endsWith("/index.html"))) {
                link.classList.add("active");
                var parent = link.parentElement;
                if (parent && parent.classList.contains("chapter-item")) {
                    parent.classList.add("expanded");
                }
                while (parent) {
                    if (parent.tagName === "LI" && parent.previousElementSibling) {
                        if (parent.previousElementSibling.classList.contains("chapter-item")) {
                            parent.previousElementSibling.classList.add("expanded");
                        }
                    }
                    parent = parent.parentElement;
                }
            }
        }
        // Track and set sidebar scroll position
        this.addEventListener('click', function(e) {
            if (e.target.tagName === 'A') {
                sessionStorage.setItem('sidebar-scroll', this.scrollTop);
            }
        }, { passive: true });
        var sidebarScrollTop = sessionStorage.getItem('sidebar-scroll');
        sessionStorage.removeItem('sidebar-scroll');
        if (sidebarScrollTop) {
            // preserve sidebar scroll position when navigating via links within sidebar
            this.scrollTop = sidebarScrollTop;
        } else {
            // scroll sidebar to current active section when navigating via "next/previous chapter" buttons
            var activeSection = document.querySelector('#sidebar .active');
            if (activeSection) {
                activeSection.scrollIntoView({ block: 'center' });
            }
        }
        // Toggle buttons
        var sidebarAnchorToggles = document.querySelectorAll('#sidebar a.toggle');
        function toggleSection(ev) {
            ev.currentTarget.parentElement.classList.toggle('expanded');
        }
        Array.from(sidebarAnchorToggles).forEach(function (el) {
            el.addEventListener('click', toggleSection);
        });
    }
}
window.customElements.define("mdbook-sidebar-scrollbox", MDBookSidebarScrollbox);
