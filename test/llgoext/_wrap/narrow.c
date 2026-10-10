typedef struct {
    long long s8;
    unsigned long long u8;
    long long s16;
    unsigned long long u16;
} narrow_values;

// This aggregate result also exercises the inserted sret parameter.
__attribute__((noinline)) narrow_values narrow_promote(signed char a, unsigned char b, short c, unsigned short d) {
    narrow_values result = {a, b, c, d};
    return result;
}

typedef narrow_values (*narrow_function)(signed char, unsigned char, short, unsigned short);
narrow_function narrow_address(void) { return narrow_promote; }

typedef signed char (*narrow_s8_callback)(int);
typedef unsigned char (*narrow_u8_callback)(int);
typedef short (*narrow_s16_callback)(int);
typedef unsigned short (*narrow_u16_callback)(int);

// Volatile hides the callback's identity so LLVM cannot fold the indirect call.
narrow_s8_callback narrow_s8_roundtrip(narrow_s8_callback fn) {
    narrow_s8_callback volatile saved = fn;
    return saved;
}
narrow_u8_callback narrow_u8_roundtrip(narrow_u8_callback fn) {
    narrow_u8_callback volatile saved = fn;
    return saved;
}
narrow_s16_callback narrow_s16_roundtrip(narrow_s16_callback fn) {
    narrow_s16_callback volatile saved = fn;
    return saved;
}
narrow_u16_callback narrow_u16_roundtrip(narrow_u16_callback fn) {
    narrow_u16_callback volatile saved = fn;
    return saved;
}
