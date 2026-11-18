/* Fixture for the native symbol cache tests: one inlined call. */
static inline __attribute__((always_inline)) int helper(int x) {
    int y = x * 7;
    y += x >> 2;
    return y ^ 0x55;
}

__attribute__((noinline)) int fix_outer(int a) {
    int r = a + 1;
    r = helper(r);
    r += 3;
    return r;
}

__attribute__((noinline)) int fix_plain(int a) {
    return a * a + 17;
}
