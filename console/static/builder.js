// builder.js — Alpine component factories.
//
// Loaded WITHOUT defer, deliberately. Alpine evaluates x-data before deferred
// scripts run, so a factory referenced by x-data must already exist when Alpine
// initialises. layout.html documents the same race for kvEditor and solves it
// by inlining; a non-deferred file is the same fix with the code somewhere you
// can read it. Order matters: this must come before alpine.min.js's deferred
// load, which every non-deferred script does by definition.
(function () {
  "use strict";

  /* dozeTags backs the tag editor: a DRAFT you submit, not controls that fire.
     rows is seeded from what the server rendered and is the only state; adding
     and removing touch it alone, and nothing reaches the service until Save.

     initial is deep-copied into both rows and base. Without the copy, reset()
     would restore references the edits had already mutated — Cancel would
     "restore" the values you were trying to abandon, which is worse than no
     Cancel at all. */
  window.dozeTags = function (initial) {
    var seed = (initial || []).map(function (t) { return { k: t.k, v: t.v }; });
    return {
      rows: seed.map(function (t) { return { k: t.k, v: t.v }; }),
      base: seed,
      dirty: false,
      reset: function () {
        this.rows = this.base.map(function (t) { return { k: t.k, v: t.v }; });
        this.dirty = false;
      },
    };
  };
})();
